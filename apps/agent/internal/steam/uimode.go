package steam

import (
	"path/filepath"
	"regexp"
	"strconv"
)

// UIModeWatcher follows logs/gameprocess_log.txt for one line type only:
// `SSGL: UI mode (A->B)` (ADR-0008 D1). Every other line, including launch
// command lines with home paths, is discarded as it is read and never kept.
// The latest B is Steam's current UI: 4 = gamepad UI (Gaming Mode or Big
// Picture), 7 = desktop UI (G15 discovery §12).
type UIModeWatcher struct {
	tail logTail
	mode int
	seen bool
}

var uiModeRe = regexp.MustCompile(`SSGL: UI mode \((\d{1,4})->(\d{1,4})\)`)

// NewUIModeWatcher watches <steamRoot>/logs/gameprocess_log.txt.
func NewUIModeWatcher(steamRoot string) *UIModeWatcher {
	return &UIModeWatcher{tail: logTail{path: filepath.Join(steamRoot, "logs", "gameprocess_log.txt"), initial: 256 << 10}}
}

// Poll reads lines appended since the last call.
func (w *UIModeWatcher) Poll() {
	w.tail.poll(func(line []byte) {
		if m := uiModeRe.FindSubmatch(line); m != nil {
			if n, err := strconv.Atoi(string(m[2])); err == nil {
				w.mode, w.seen = n, true
			}
		}
	})
}

// Mode returns "gamepad", "desktop", "unknown:<n>" or "" (no line seen yet).
func (w *UIModeWatcher) Mode() string {
	if !w.seen {
		return ""
	}
	switch w.mode {
	case 4:
		return "gamepad"
	case 7:
		return "desktop"
	}
	return "unknown:" + strconv.Itoa(w.mode)
}
