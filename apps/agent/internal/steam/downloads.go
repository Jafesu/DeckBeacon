package steam

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Live download tracking (ADR-0008 D1 amendment, G15 discovery §13).
//
// Steam keeps no live byte counter on disk: the appmanifest, the downloading/
// tree and content_log carry exact byte counts only at events (start, sub-step,
// pause, resume, completion). Live values are therefore assembled:
//   - exact anchor: the `update started : download A/B, … stage C/D` line, or
//     without one (agent started mid-download, line rotated away) the
//     manifest's last snapshot;
//   - live progress: Steam's own disk writes since the anchor (/proc/<pid>/io
//     write_bytes, which stops the moment a download pauses), capped at D;
//   - rate: the write_bytes rate scaled from staged to downloaded bytes, or
//     Steam's `Current download rate: X Mbps` line (about once a minute);
//   - state: `state changed` / `App update changed` / `scheduler finished`,
//     else inferred from whether Steam is writing at download speed.
//
// content_log also names LAN peers, CDN hosts with IPs and install paths: only
// the anchored patterns below are matched, and only numbers and a fixed
// vocabulary are kept. No log text is stored.

var (
	clTime     = regexp.MustCompile(`^\[(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d)\] `)
	clStarted  = regexp.MustCompile(`^AppID (\d{1,10}) update started : download (\d{1,20})/(\d{1,20}), store \d+/\d+, reuse \d+/\d+, delta \d+/\d+, stage (\d{1,20})/(\d{1,20})\s*$`)
	clState    = regexp.MustCompile(`^AppID (\d{1,10}) state changed : ([A-Za-z ,()]{0,200})$`)
	clApp      = regexp.MustCompile(`^AppID (\d{1,10}) App update changed : ([A-Za-z ,]{0,200})$`)
	clRemoved  = regexp.MustCompile(`^AppID (\d{1,10}) scheduler finished : removed from schedule \(result No Error`)
	clRate     = regexp.MustCompile(`^Current download rate: (\d{1,7}(?:\.\d{1,6})?) Mbps\s*$`)
	clIncrease = regexp.MustCompile(`^Increasing target number of download connections to \d+ \(rate was [\d.]+, now (\d{1,7}(?:\.\d{1,6})?)\)\s*$`)
)

// LiveDownload is the tracked state of one app's download.
type LiveDownload struct {
	AppID      uint32
	Phase      string // downloading | paused | installing | verifying | updating | manifest status
	Downloaded uint64 // estimated live, exact at anchors
	Total      uint64
	Staged     uint64
	StageTotal uint64
	Estimated  bool    // progress extrapolated from disk writes since the anchor
	RateBps    float64 // download bytes/s (0 = unknown)
	RateSource string

	anchorWrite      uint64
	anchorOK         bool
	anchorDownloaded uint64
	phaseKnown       bool // phase came from a log line
	seeded           bool // anchor came from the manifest, not the log
	seedDownloaded   uint64
}

// Snapshot is the appmanifest's last exact byte counts for an app.
type Snapshot struct {
	Downloaded, Total, Staged, StageTotal uint64
	Status                                string // from StateFlags
}

// DownloadTracker follows content_log.txt and Steam's write counter.
type DownloadTracker struct {
	tail      logTail
	apps      map[uint32]*LiveDownload
	active    uint32
	logRate   float64 // bytes/s from the last rate line
	logRateAt time.Time
	lastWrite uint64
	lastAt    time.Time
	writeOK   bool
	ioRate    float64 // smoothed Steam write bytes/s
	started   bool
	now       func() time.Time
}

// NewDownloadTracker watches <steamRoot>/logs/content_log.txt.
func NewDownloadTracker(steamRoot string, now func() time.Time) *DownloadTracker {
	if now == nil {
		now = time.Now
	}
	return &DownloadTracker{tail: logTail{path: filepath.Join(steamRoot, "logs", "content_log.txt"), initial: 512 << 10}, apps: map[uint32]*LiveDownload{}, now: now}
}

// Poll consumes new log lines, then folds in Steam's current write counter.
// On the first poll the end of the rotated content_log.previous.txt is read
// first, so a download that started before the last rotation is still known.
func (t *DownloadTracker) Poll(steamWrite uint64, writeOK bool) {
	if !t.started {
		t.started = true
		prev := logTail{path: strings.TrimSuffix(t.tail.path, ".txt") + ".previous.txt", initial: t.tail.initial}
		prev.poll(t.line)
	}
	t.tail.poll(t.line)
	t.sample(steamWrite, writeOK)
}

func (t *DownloadTracker) app(id uint32) *LiveDownload {
	d := t.apps[id]
	if d == nil {
		d = &LiveDownload{AppID: id}
		t.apps[id] = d
	}
	return d
}

func atoi(s string) uint64 { v, _ := strconv.ParseUint(s, 10, 64); return v }

func (t *DownloadTracker) line(raw []byte) {
	// Real content_log lines end in CRLF, and the `update started` line also
	// has a space before the CR (G15, `cat -A`): trim all trailing whitespace.
	s := strings.TrimRight(string(raw), " \t\r")
	m := clTime.FindStringSubmatch(s)
	if m == nil {
		return
	}
	at := t.now()
	if ts, err := time.ParseInLocation("2006-01-02 15:04:05", m[1], time.Local); err == nil {
		at = ts
	}
	s = s[len(m[0]):]
	if m := clStarted.FindStringSubmatch(s); m != nil {
		d := t.app(uint32(atoi(m[1])))
		done, total, staged, stageTotal := atoi(m[2]), atoi(m[3]), atoi(m[4]), atoi(m[5])
		// Small sub-steps (preload, 64-byte steps) run inside one update: keep
		// the largest total as the progress basis (§13.3/§13.5).
		if total == 0 || (!d.seeded && total < d.Total) {
			return
		}
		d.Downloaded, d.Total, d.Staged, d.StageTotal, d.Estimated = done, total, staged, stageTotal, false
		d.anchorDownloaded, d.seeded = done, false
		d.anchorWrite, d.anchorOK = t.lastWrite, t.writeOK
		t.active = d.AppID
		return
	}
	if m := clState.FindStringSubmatch(s); m != nil {
		id, flags := uint32(atoi(m[1])), m[2]
		switch {
		case strings.Contains(flags, "(Suspended)"):
			d := t.app(id)
			d.Phase, d.phaseKnown = "paused", true
			t.active = id
		case strings.Contains(flags, "Update Running"):
			if d := t.app(id); !d.phaseKnown || d.Phase == "paused" {
				d.Phase, d.phaseKnown = "updating", true
			}
			t.active = id
		case strings.HasPrefix(flags, "Fully Installed,") && !strings.Contains(flags, "Update"):
			t.finish(id)
		}
		return
	}
	if m := clApp.FindStringSubmatch(s); m != nil {
		d, ph := t.app(uint32(atoi(m[1]))), m[2]
		switch {
		case d.Phase == "paused" && d.phaseKnown: // "Stopping"/"None" after a pause
		case strings.Contains(ph, "Committing"):
			d.Phase, d.phaseKnown = "installing", true
		case strings.Contains(ph, "Downloading"):
			d.Phase, d.phaseKnown = "downloading", true
		case strings.Contains(ph, "Verifying"):
			d.Phase, d.phaseKnown = "verifying", true
		case strings.Contains(ph, "Running Update"):
			d.Phase, d.phaseKnown = "updating", true
		}
		return
	}
	if m := clRemoved.FindStringSubmatch(s); m != nil {
		t.finish(uint32(atoi(m[1])))
		return
	}
	if m := clRate.FindStringSubmatch(s); m != nil {
		t.setLogRate(m[1], at)
		return
	}
	if m := clIncrease.FindStringSubmatch(s); m != nil {
		t.setLogRate(m[1], at)
	}
}

func (t *DownloadTracker) setLogRate(mbps string, at time.Time) {
	v, err := strconv.ParseFloat(mbps, 64)
	if err != nil {
		return
	}
	t.logRate, t.logRateAt = v*1e6/8, at // Steam logs megabits per second (§13.3)
}

func (t *DownloadTracker) finish(id uint32) {
	delete(t.apps, id)
	if t.active == id {
		t.active = 0
	}
}

func (t *DownloadTracker) sample(write uint64, ok bool) {
	now := t.now()
	if ok && t.writeOK && write >= t.lastWrite {
		dt := now.Sub(t.lastAt).Seconds()
		if dt < 1 {
			return // too soon: keep the previous base so dt grows
		}
		inst := float64(write-t.lastWrite) / dt
		if t.ioRate == 0 {
			t.ioRate = inst
		} else {
			t.ioRate = 0.5*t.ioRate + 0.5*inst
		}
	} else {
		t.ioRate = 0 // first sample, counter reset (Steam restarted) or unreadable
		for _, d := range t.apps {
			d.anchorOK = false
		}
	}
	t.lastWrite, t.lastAt, t.writeOK = write, now, ok
	if ok { // anchors read before the counter was available start from here
		for _, d := range t.apps {
			if !d.anchorOK && d.Total > 0 {
				d.anchorWrite, d.anchorOK = write, true
			}
		}
	}
}

// ActiveApp is the app Steam's log says it is updating (0 = none known).
func (t *DownloadTracker) ActiveApp() uint32 { return t.active }

// writesFlowing means Steam is writing at download-like speed (> 1 MiB/s).
func (t *DownloadTracker) writesFlowing() bool { return t.writeOK && t.ioRate > 1<<20 }

// Live combines the log's exact anchor (or, without one, the manifest's last
// snapshot) with Steam's disk writes since then.
func (t *DownloadTracker) Live(appID uint32, man Snapshot) LiveDownload {
	d := t.app(appID)
	reseed := d.seeded && man.Total > 0 && (man.Downloaded != d.seedDownloaded || man.Total != d.Total)
	if (d.Total == 0 || reseed) && man.Total > 0 { // no log anchor, or a newer manifest write
		d.Downloaded, d.Total, d.Staged, d.StageTotal = man.Downloaded, man.Total, man.Staged, man.StageTotal
		d.anchorDownloaded, d.seedDownloaded, d.seeded, d.Estimated = man.Downloaded, man.Downloaded, true, false
		d.anchorWrite, d.anchorOK = t.lastWrite, t.writeOK
	}
	if !d.anchorOK && t.writeOK { // anchor taken before the first counter sample
		d.anchorWrite, d.anchorOK = t.lastWrite, true
	}
	if !d.phaseKnown {
		// No phase line seen: writes at download speed mean downloading;
		// otherwise keep the manifest's status (it cannot show paused, §13.1).
		if t.writesFlowing() {
			d.Phase = "downloading"
		} else {
			d.Phase = man.Status
		}
	}
	if d.Phase == "downloading" && d.anchorOK && d.StageTotal > 0 && t.lastWrite > d.anchorWrite {
		// Steam's writes are staged (decompressed) bytes: advance from the anchor
		// by the bytes written, mapped to download bytes by the update's
		// download/stage ratio. At the anchor itself the exact counts stand.
		ratio := float64(d.Total) / float64(d.StageTotal)
		extra := uint64(float64(min(t.lastWrite-d.anchorWrite, d.StageTotal-min(d.Staged, d.StageTotal))) * ratio)
		d.Downloaded = min(d.Total, d.anchorDownloaded+extra)
		d.Estimated = true
	}
	switch {
	case d.Phase != "downloading":
		d.RateBps, d.RateSource = 0, ""
	case t.writeOK && t.ioRate > 0 && d.StageTotal > 0:
		d.RateBps = t.ioRate * float64(d.Total) / float64(d.StageTotal)
		d.RateSource = "steam disk writes (/proc io)"
	case t.now().Sub(t.logRateAt) < 90*time.Second && t.logRate > 0:
		d.RateBps, d.RateSource = t.logRate, "steam content_log rate"
	default:
		d.RateBps, d.RateSource = 0, ""
	}
	return *d
}
