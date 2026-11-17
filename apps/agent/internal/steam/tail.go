package steam

import (
	"bytes"
	"io"
	"os"
)

// logTail follows an append-only Steam log that Steam rotates to
// *.previous.txt or truncates. Each complete line is handed to the caller and
// then dropped: tailers keep no log text, only what their matchers extract.
type logTail struct {
	path    string
	initial int64 // bytes read from the end on first open
	offset  int64
	ident   os.FileInfo
	rest    []byte
}

const (
	tailMaxChunk = 1 << 20
	tailMaxLine  = 4 << 10
)

// poll reads lines appended since the last call.
func (t *logTail) poll(line func([]byte)) {
	f, err := os.Open(t.path)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return
	}
	switch {
	case t.ident == nil:
		t.offset, t.rest = max(0, st.Size()-t.initial), nil
	case !os.SameFile(t.ident, st) || st.Size() < t.offset:
		t.offset, t.rest = 0, nil
	}
	t.ident = st
	for st.Size() > t.offset {
		if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
			return
		}
		buf, err := io.ReadAll(io.LimitReader(f, tailMaxChunk))
		if err != nil || len(buf) == 0 {
			return
		}
		t.offset += int64(len(buf))
		data := append(t.rest, buf...)
		last := bytes.LastIndexByte(data, '\n')
		if last < 0 {
			t.rest = capRest(data)
			continue
		}
		t.rest = capRest(data[last+1:])
		for _, l := range bytes.Split(data[:last], []byte{'\n'}) {
			line(l)
		}
	}
}

func capRest(b []byte) []byte {
	if len(b) > tailMaxLine {
		return nil // an over-long partial line is not one we match
	}
	return append([]byte(nil), b...)
}
