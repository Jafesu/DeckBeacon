package steam

import (
	"bytes"
	"io"
	"os"
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
	path   string
	offset int64
	ident  os.FileInfo
	rest   []byte
	mode   int
	seen   bool
}

const (
	uiModeInitialTail = 256 << 10
	uiModeMaxChunk    = 1 << 20
	uiModeMaxLine     = 4 << 10
)

var uiModeRe = regexp.MustCompile(`SSGL: UI mode \((\d{1,4})->(\d{1,4})\)`)

// NewUIModeWatcher watches <steamRoot>/logs/gameprocess_log.txt.
func NewUIModeWatcher(steamRoot string) *UIModeWatcher {
	return &UIModeWatcher{path: filepath.Join(steamRoot, "logs", "gameprocess_log.txt")}
}

// Poll reads lines appended since the last call. It restarts from the top
// when the file was replaced or truncated (Steam rotates to *.previous.txt).
func (w *UIModeWatcher) Poll() {
	f, err := os.Open(w.path)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return
	}
	switch {
	case w.ident == nil: // first read: the tail is enough to find the last switch
		w.offset = max(0, st.Size()-uiModeInitialTail)
		w.rest = nil
	case !os.SameFile(w.ident, st) || st.Size() < w.offset:
		w.offset, w.rest = 0, nil
	}
	w.ident = st
	if st.Size() == w.offset {
		return
	}
	if _, err := f.Seek(w.offset, io.SeekStart); err != nil {
		return
	}
	buf, err := io.ReadAll(io.LimitReader(f, uiModeMaxChunk))
	if err != nil {
		return
	}
	w.offset += int64(len(buf))
	data := append(w.rest, buf...)
	last := bytes.LastIndexByte(data, '\n')
	if last < 0 {
		w.rest = capRest(data)
		return
	}
	w.rest = capRest(data[last+1:])
	for _, line := range bytes.Split(data[:last], []byte{'\n'}) {
		if m := uiModeRe.FindSubmatch(line); m != nil {
			if n, err := strconv.Atoi(string(m[2])); err == nil {
				w.mode, w.seen = n, true
			}
		}
	}
}

func capRest(b []byte) []byte {
	if len(b) > uiModeMaxLine {
		return nil // an over-long partial line is not a UI mode line
	}
	return append([]byte(nil), b...)
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
