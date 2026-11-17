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
//   - exact anchor: the `update started : download A/B, … stage C/D` line;
//   - live progress: Steam's own disk writes since the anchor (/proc/<pid>/io
//     write_bytes, which stops the moment a download pauses), capped at D;
//   - rate: the write_bytes rate scaled from staged to downloaded bytes, or
//     Steam's `Current download rate: X Mbps` line (about once a minute);
//   - state: `state changed` / `App update changed` / `scheduler finished`.
//
// content_log also names LAN peers, CDN hosts with IPs and install paths: only
// the anchored patterns below are matched, and only numbers and a fixed
// vocabulary are kept. No log text is stored.

var (
	clTime     = regexp.MustCompile(`^\[(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d)\] `)
	clStarted  = regexp.MustCompile(`^AppID (\d{1,10}) update started : download (\d{1,20})/(\d{1,20}), store \d+/\d+, reuse \d+/\d+, delta \d+/\d+, stage (\d{1,20})/(\d{1,20})$`)
	clState    = regexp.MustCompile(`^AppID (\d{1,10}) state changed : ([A-Za-z ,()]{0,200})$`)
	clApp      = regexp.MustCompile(`^AppID (\d{1,10}) App update changed : ([A-Za-z ,]{0,200})$`)
	clRemoved  = regexp.MustCompile(`^AppID (\d{1,10}) scheduler finished : removed from schedule \(result No Error`)
	clRate     = regexp.MustCompile(`^Current download rate: (\d{1,7}(?:\.\d{1,6})?) Mbps$`)
	clIncrease = regexp.MustCompile(`^Increasing target number of download connections to \d+ \(rate was [\d.]+, now (\d{1,7}(?:\.\d{1,6})?)\)$`)
)

// LiveDownload is the tracked state of one app's download.
type LiveDownload struct {
	AppID      uint32
	Phase      string // downloading | paused | installing | verifying | updating | done
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
func (t *DownloadTracker) Poll(steamWrite uint64, writeOK bool) {
	t.tail.poll(t.line)
	t.sample(steamWrite, writeOK)
}

func (t *DownloadTracker) app(id uint32) *LiveDownload {
	d := t.apps[id]
	if d == nil {
		d = &LiveDownload{AppID: id, Phase: "updating"}
		t.apps[id] = d
	}
	return d
}

func atoi(b []byte) uint64 { v, _ := strconv.ParseUint(string(b), 10, 64); return v }

func (t *DownloadTracker) line(raw []byte) {
	s := strings.TrimRight(string(raw), "\r")
	at := t.now()
	if m := clTime.FindStringSubmatch(s); m != nil {
		if ts, err := time.ParseInLocation("2006-01-02 15:04:05", m[1], time.Local); err == nil {
			at = ts
		}
		s = s[len(m[0]):]
	} else {
		return
	}
	if m := clStarted.FindStringSubmatch(s); m != nil {
		d := t.app(uint32(atoi([]byte(m[1]))))
		done, total, staged, stageTotal := atoi([]byte(m[2])), atoi([]byte(m[3])), atoi([]byte(m[4])), atoi([]byte(m[5]))
		// Small sub-steps (preload, 64-byte steps) run inside one update: keep
		// the largest total as the progress basis (§13.3/§13.5).
		if total == 0 || total < d.Total {
			return
		}
		d.Downloaded, d.Total, d.Staged, d.StageTotal, d.Estimated = done, total, staged, stageTotal, false
		d.anchorDownloaded = done
		d.anchorWrite, d.anchorOK = t.lastWrite, t.writeOK
		t.active = d.AppID
		return
	}
	if m := clState.FindStringSubmatch(s); m != nil {
		id := uint32(atoi([]byte(m[1])))
		flags := m[2]
		switch {
		case strings.Contains(flags, "(Suspended)"):
			t.app(id).Phase = "paused"
		case strings.Contains(flags, "Update Running"):
			if d := t.app(id); d.Phase == "paused" || d.Phase == "done" {
				d.Phase = "updating"
			}
			t.active = id
		case strings.HasPrefix(flags, "Fully Installed,") && !strings.Contains(flags, "Update"):
			t.finish(id)
		}
		return
	}
	if m := clApp.FindStringSubmatch(s); m != nil {
		id := uint32(atoi([]byte(m[1])))
		d, ok := t.apps[id]
		if !ok {
			return
		}
		ph := m[2]
		switch {
		case d.Phase == "paused":
		case strings.Contains(ph, "Committing"):
			d.Phase = "installing"
		case strings.Contains(ph, "Downloading"):
			d.Phase = "downloading"
		case strings.Contains(ph, "Verifying"):
			d.Phase = "verifying"
		case strings.Contains(ph, "Running Update"):
			d.Phase = "updating"
		}
		return
	}
	if m := clRemoved.FindStringSubmatch(s); m != nil {
		t.finish(uint32(atoi([]byte(m[1]))))
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
		if dt := now.Sub(t.lastAt).Seconds(); dt >= 1 {
			inst := float64(write-t.lastWrite) / dt
			if t.ioRate == 0 {
				t.ioRate = inst
			} else {
				t.ioRate = 0.5*t.ioRate + 0.5*inst
			}
		} else {
			return // too soon: keep the previous base so dt grows
		}
	} else {
		t.ioRate = 0 // first sample, counter reset (Steam restarted) or unreadable
		for _, d := range t.apps {
			d.anchorOK = false
		}
	}
	t.lastWrite, t.lastAt, t.writeOK = write, now, ok
	d := t.apps[t.active]
	if d == nil {
		return
	}
	if !d.anchorOK && ok { // anchor seen before the first counter sample
		d.anchorWrite, d.anchorOK = write, true
	}
	if d.Phase == "downloading" && d.anchorOK && d.StageTotal > 0 && write > d.anchorWrite {
		// Steam's writes are staged (decompressed) bytes: advance from the exact
		// anchor by the written bytes, mapped to download bytes by the update's
		// download/stage ratio. At the anchor itself the exact counts stand.
		ratio := float64(d.Total) / float64(d.StageTotal)
		extra := uint64(float64(min(write-d.anchorWrite, d.StageTotal-min(d.Staged, d.StageTotal))) * ratio)
		d.Downloaded = min(d.Total, d.anchorDownloaded+extra)
		d.Estimated = true
	}
	switch {
	case d.Phase != "downloading":
		d.RateBps, d.RateSource = 0, ""
	case ok && t.ioRate > 0 && d.StageTotal > 0:
		d.RateBps = t.ioRate * float64(d.Total) / float64(d.StageTotal)
		d.RateSource = "steam disk writes (/proc io)"
	case now.Sub(t.logRateAt) < 90*time.Second && t.logRate > 0:
		d.RateBps, d.RateSource = t.logRate, "steam content_log rate"
	default:
		d.RateBps, d.RateSource = 0, ""
	}
}

// Active returns the download Steam is working on (running or paused), if any.
func (t *DownloadTracker) Active() *LiveDownload {
	d := t.apps[t.active]
	if d == nil || d.Total == 0 {
		return nil
	}
	c := *d
	return &c
}
