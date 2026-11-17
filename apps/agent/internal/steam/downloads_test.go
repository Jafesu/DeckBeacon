package steam

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Line formats and values recorded on the G15 (discovery §13.3), with the
// privacy-bearing lines that must be ignored.
const recordedStart = `[2026-11-16 21:06:46] AppID 271590 state changed : Update Required,
[2026-11-16 21:06:46] AppID 271590 state changed : Update Required,Update Queued,Update Running,
[2026-11-16 21:06:47] Created peer download interface to host CANARY-LAN-PEER (192.0.2.7)
[2026-11-16 21:06:47] HTTPS (SteamCache,510) - cache7-CANARYPOP.steamcontent.com (198.51.100.9:443 / 198.51.100.9:443, host: cache7-CANARYPOP.steamcontent.com)
[2026-11-16 21:06:49] AppID 271590 update started : download 0/124602905312, store 0/0, reuse 0/0, delta 0/0, stage 0/129095667634
[2026-11-16 21:06:54] AppID 271590 update started : download 0/350260960, store 0/0, reuse 0/0, delta 0/0, stage 0/370203254
[2026-11-16 21:07:03] AppID 271590 starting commit from "/home/CANARY_USER/x" to "/home/CANARY_USER/y" : 4 updated, 0 moved, 0 deleted files
[2026-11-16 21:07:10] AppID 271590 update started : download 0/64, store 0/0, reuse 0/0, delta 0/0, stage 0/16
[2026-11-16 21:07:10] AppID 271590 update started : download 87346528/124602905312, store 0/0, reuse 0/0, delta 0/0, stage 116507336/129095667634
[2026-11-16 21:07:10] AppID 271590 App update changed : Running Update,Downloading,Staging,
[2026-11-16 21:07:10] AppID 271590 Shader update changed : Running Update,Committing,
[2026-11-16 21:07:19] Current download rate: 315.169 Mbps
`

const recordedPause = `[2026-11-16 21:08:44] AppID 271590 update canceled : Disabled (Suspended)
[2026-11-16 21:08:44] AppID 271590 App update changed : Running Update,Downloading,Staging,Stopping,
[2026-11-16 21:08:44] AppID 271590 App update changed : None
[2026-11-16 21:08:44] AppID 271590 state changed : Update Required,Update Queued,Update Started, (Suspended)
[2026-11-16 21:09:06] stats: (PeerServer, 1) CANARY-LAN-PEER: 2878626683 Bytes, 58 sec (394.13 Mbps). 1 Hits / 0 Misses
`

const recordedResume = `[2026-11-16 21:09:01] AppID 271590 state changed : Update Required,Update Queued,Update Running,Update Started,
[2026-11-16 21:09:01] AppID 271590 App update changed : Running Update,Reconfiguring,
[2026-11-16 21:09:01] AppID 271590 update started : download 4464269120/124602905312, store 0/0, reuse 0/0, delta 0/0, stage 5157784264/129095667634
[2026-11-16 21:09:02] AppID 271590 App update changed : Running Update,Downloading,Staging,
`

const recordedDone = `[2026-11-16 23:59:00] AppID 271590 App update changed : Running Update,Committing,
[2026-11-16 23:59:10] AppID 271590 state changed : Fully Installed,
[2026-11-16 23:59:10] AppID 271590 scheduler finished : removed from schedule (result No Error, state 0xc)
`

func TestDownloadTrackerRecordedSequence(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "logs")
	_ = os.MkdirAll(logs, 0o755)
	path := filepath.Join(logs, "content_log.txt")
	_ = os.WriteFile(path, nil, 0o644)
	clock := time.Date(2026, 11, 17, 3, 7, 20, 0, time.UTC)
	tr := NewDownloadTracker(root, func() time.Time { return clock })
	write := uint64(10_000_000_000)
	step := func(text string, adv time.Duration, wrote uint64) {
		if text != "" {
			f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			_, _ = f.WriteString(realLineEndings(text))
			_ = f.Close()
		}
		clock = clock.Add(adv)
		write += wrote
		tr.Poll(write, true)
	}
	step(recordedStart, 0, 0)
	live := func() LiveDownload { return tr.Live(271590, Snapshot{Status: "updating"}) }
	d := live()
	if d.AppID != 271590 || d.Total != 124602905312 || d.Downloaded != 87346528 || d.Phase != "downloading" {
		t.Fatalf("anchor after sub-steps: %+v", d)
	}
	// 3 s at Steam's measured 53.5 MB/s staged write rate (§13.4).
	step("", 3*time.Second, 160_500_000)
	step("", 3*time.Second, 160_500_000)
	d = live()
	if !d.Estimated || d.Downloaded <= 87346528 {
		t.Fatalf("live progress must advance from disk writes: %+v", d)
	}
	if mbps := d.RateBps * 8 / 1e6; mbps < 380 || mbps > 440 {
		t.Fatalf("rate %.1f Mbps, want about 413 (download bytes = staged / 1.036)", mbps)
	}
	step(recordedPause, 3*time.Second, 1_000_000)
	if d = live(); d.Phase != "paused" || d.RateBps != 0 {
		t.Fatalf("pause: %+v", d)
	}
	step(recordedResume, 3*time.Second, 0)
	if d = live(); d.Phase != "downloading" || d.Downloaded != 4464269120 || d.Estimated {
		t.Fatalf("resume re-anchors on the exact counts: %+v", d)
	}
	step(recordedDone, 3*time.Second, 0)
	if tr.ActiveApp() != 0 {
		t.Fatal("finished download must clear")
	}
	if dump := strings.Join([]string{string(tr.tail.rest)}, ""); strings.Contains(dump, "CANARY") {
		t.Fatal("no log text may be retained")
	}
}

func TestDownloadTrackerLogRateFallback(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "logs"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "logs", "content_log.txt"), []byte(recordedStart), 0o644)
	clock := time.Date(2026, 11, 16, 21, 7, 30, 0, time.Local)
	tr := NewDownloadTracker(root, func() time.Time { return clock })
	tr.Poll(0, false) // /proc/<steam>/io unreadable
	d := tr.Live(271590, Snapshot{})
	if d.RateSource != "steam content_log rate" || int(d.RateBps*8/1e6) != 315 {
		t.Fatalf("fallback rate: %+v", d)
	}
	clock = clock.Add(5 * time.Minute) // stale rate line
	tr.Poll(0, false)
	if d := tr.Live(271590, Snapshot{}); d.RateBps != 0 {
		t.Fatalf("stale rate must not be shown: %+v", d)
	}
}

func TestReadIOPicksSteamClient(t *testing.T) {
	proc := t.TempDir()
	mk := func(pid, comm, io string) {
		_ = os.MkdirAll(filepath.Join(proc, pid), 0o755)
		_ = os.WriteFile(filepath.Join(proc, pid, "comm"), []byte(comm+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(proc, pid, "io"), []byte(io), 0o644)
	}
	mk("10", "steam", "rchar: 1\nwchar: 50\nsyscr: 1\nsyscw: 1\nread_bytes: 0\nwrite_bytes: 5\ncancelled_write_bytes: 0\n")
	mk("6225", "steam", "rchar: 1\nwchar: 9000\nsyscr: 1\nsyscw: 1\nread_bytes: 0\nwrite_bytes: 777\ncancelled_write_bytes: 0\n")
	mk("11", "steamwebhelper", "wchar: 99999\nwrite_bytes: 99999\n")
	p := ScanProcs(proc)
	if !p.SteamWriteOK || p.SteamWrite != 777 {
		t.Fatalf("got %d %v", p.SteamWrite, p.SteamWriteOK)
	}
}

// G15 hardware run: the agent was restarted mid-download, the log held no
// "update started" line for the app, and pausing changed nothing. Now the
// manifest seeds the anchor, writes give speed and progress, and a pause line
// applies even without an anchor.
func TestDownloadTrackerMidDownloadWithoutLogAnchor(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "logs")
	_ = os.MkdirAll(logs, 0o755)
	path := filepath.Join(logs, "content_log.txt")
	_ = os.WriteFile(path, []byte("[2026-11-16 21:40:00] unrelated line\n"), 0o644)
	clock := time.Date(2026, 11, 17, 4, 0, 0, 0, time.UTC)
	tr := NewDownloadTracker(root, func() time.Time { return clock })
	// Manifest last written at the previous pause/resume: 70.5 % of 124.6 GB.
	man := Snapshot{Downloaded: 87_844_948_000, Total: 124_602_905_312, Staged: 91_000_000_000, StageTotal: 129_095_667_634, Status: "updating"}
	w := uint64(500_000_000_000)
	tr.Poll(w, true)
	if d := tr.Live(271590, man); d.Phase != "updating" || d.RateBps != 0 {
		t.Fatalf("before writes flow: %+v", d)
	}
	for i := 0; i < 3; i++ { // ~37.5 MB/s staged (300 Mbps)
		clock = clock.Add(2 * time.Second)
		w += 75_000_000
		tr.Poll(w, true)
	}
	d := tr.Live(271590, man)
	if d.Phase != "downloading" || d.Downloaded <= man.Downloaded || !d.Estimated {
		t.Fatalf("seeded live progress: %+v", d)
	}
	if mbps := d.RateBps * 8 / 1e6; mbps < 270 || mbps > 310 {
		t.Fatalf("rate %.0f Mbps", mbps)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("[2026-11-16 22:00:00] AppID 271590 state changed : Update Required,Update Queued,Update Started, (Suspended)\n")
	_ = f.Close()
	clock = clock.Add(2 * time.Second)
	tr.Poll(w, true)
	if d := tr.Live(271590, man); d.Phase != "paused" || d.RateBps != 0 || tr.ActiveApp() != 271590 {
		t.Fatalf("pause without an anchor: %+v", d)
	}
}

func TestDownloadTrackerReadsRotatedLogAtStart(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "logs")
	_ = os.MkdirAll(logs, 0o755)
	_ = os.WriteFile(filepath.Join(logs, "content_log.previous.txt"), []byte(recordedStart), 0o644)
	_ = os.WriteFile(filepath.Join(logs, "content_log.txt"), []byte("[2026-11-16 21:30:00] Current download rate: 300.0 Mbps\n"), 0o644)
	tr := NewDownloadTracker(root, nil)
	tr.Poll(0, false)
	if d := tr.Live(271590, Snapshot{}); tr.ActiveApp() != 271590 || d.Total != 124602905312 || d.Phase != "downloading" {
		t.Fatalf("anchor from rotated log: %+v", d)
	}
}

// realLineEndings reproduces the G15's bytes: CRLF everywhere, plus a space
// before the CR on `update started` lines (`... stage C/D ^M$` in cat -A).
func realLineEndings(text string) string {
	var b strings.Builder
	for _, l := range strings.SplitAfter(text, "\n") {
		if l == "" {
			continue
		}
		l = strings.TrimSuffix(l, "\n")
		if strings.Contains(l, " update started : ") {
			l += " "
		}
		b.WriteString(l + "\r\n")
	}
	return b.String()
}
