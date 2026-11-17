package steam

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Shortcut is a non-Steam game added to Steam (userdata/*/config/shortcuts.vdf).
// Only the ADR-0008 D2 allowlist is kept: appid, AppName, LastPlayTime,
// IsHidden. exe, StartDir, LaunchOptions, ShortcutPath, icon and tags hold
// home paths and arguments; the parser skips them without storing them.
type Shortcut struct {
	AppID      uint32
	Name       string
	LastPlayed time.Time
	Hidden     bool
}

const (
	maxShortcutsBytes = 1 << 20
	maxShortcutDepth  = 8
	maxShortcutName   = 256
)

// errShortcuts never carries a path or a value: the userdata directory name is
// a Steam account id (ADR-0008 D2).
var errShortcuts = errors.New("shortcuts.vdf: malformed")

// Binary KeyValues type bytes.
const (
	bvMap    = 0x00
	bvString = 0x01
	bvInt32  = 0x02
	bvUint64 = 0x07
	bvEnd    = 0x08
)

// ParseShortcuts decodes binary shortcuts.vdf content.
func ParseShortcuts(b []byte) ([]Shortcut, error) {
	if len(b) > maxShortcutsBytes {
		return nil, errShortcuts
	}
	r := &bvReader{b: b}
	// root: 0x00 "shortcuts" { "0" {entry} "1" {entry} ... } 0x08 [0x08]
	t, ok := r.byte()
	if !ok || t != bvMap {
		return nil, errShortcuts
	}
	if name, ok := r.cstr(); !ok || !strings.EqualFold(name, "shortcuts") {
		return nil, errShortcuts
	}
	var out []Shortcut
	for {
		t, ok := r.byte()
		if !ok {
			return nil, errShortcuts
		}
		if t == bvEnd {
			break
		}
		if _, ok := r.cstr(); !ok { // index key "0", "1", ...
			return nil, errShortcuts
		}
		if t != bvMap {
			if !r.skipValue(t, 1) {
				return nil, errShortcuts
			}
			continue
		}
		s, ok := r.entry()
		if !ok {
			return nil, errShortcuts
		}
		if s.AppID != 0 {
			out = append(out, s)
		}
	}
	return out, nil
}

func (r *bvReader) entry() (Shortcut, bool) {
	var s Shortcut
	for {
		t, ok := r.byte()
		if !ok {
			return s, false
		}
		if t == bvEnd {
			return s, true
		}
		key, ok := r.cstr()
		if !ok {
			return s, false
		}
		switch k := strings.ToLower(key); {
		case k == "appid" && t == bvInt32:
			v, ok := r.u32()
			if !ok {
				return s, false
			}
			s.AppID = v // stored as signed int32; the bits are the u32 key
		case k == "appname" && t == bvString:
			v, ok := r.cstr()
			if !ok {
				return s, false
			}
			s.Name = capText(v, maxShortcutName)
		case k == "lastplaytime" && t == bvInt32:
			v, ok := r.u32()
			if !ok {
				return s, false
			}
			if v != 0 {
				s.LastPlayed = time.Unix(int64(v), 0).UTC()
			}
		case k == "ishidden" && t == bvInt32:
			v, ok := r.u32()
			if !ok {
				return s, false
			}
			s.Hidden = v != 0
		default:
			if !r.skipValue(t, 2) {
				return s, false
			}
		}
	}
}

type bvReader struct {
	b   []byte
	pos int
}

func (r *bvReader) byte() (byte, bool) {
	if r.pos >= len(r.b) {
		return 0, false
	}
	r.pos++
	return r.b[r.pos-1], true
}

func (r *bvReader) cstr() (string, bool) {
	i := r.pos
	for i < len(r.b) && r.b[i] != 0 {
		i++
	}
	if i >= len(r.b) {
		return "", false
	}
	s := string(r.b[r.pos:i])
	r.pos = i + 1
	return s, true
}

func (r *bvReader) skipCstr() bool {
	for r.pos < len(r.b) {
		if r.b[r.pos] == 0 {
			r.pos++
			return true
		}
		r.pos++
	}
	return false
}

func (r *bvReader) u32() (uint32, bool) {
	if r.pos+4 > len(r.b) {
		return 0, false
	}
	v := binary.LittleEndian.Uint32(r.b[r.pos:])
	r.pos += 4
	return v, true
}

// skipValue discards a value of type t without retaining it.
func (r *bvReader) skipValue(t byte, depth int) bool {
	switch t {
	case bvString:
		return r.skipCstr()
	case bvInt32:
		if r.pos+4 > len(r.b) {
			return false
		}
		r.pos += 4
		return true
	case bvUint64:
		if r.pos+8 > len(r.b) {
			return false
		}
		r.pos += 8
		return true
	case bvMap:
		if depth >= maxShortcutDepth {
			return false
		}
		for {
			c, ok := r.byte()
			if !ok {
				return false
			}
			if c == bvEnd {
				return true
			}
			if !r.skipCstr() || !r.skipValue(c, depth+1) {
				return false
			}
		}
	default:
		return false
	}
}

func capText(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// ShortcutFiles lists userdata/*/config/shortcuts.vdf: the only file DeckBeacon
// reads under userdata (ADR-0008 D2).
func ShortcutFiles(steamRoot string) []string {
	files, _ := filepath.Glob(filepath.Join(steamRoot, "userdata", "*", "config", "shortcuts.vdf"))
	sort.Strings(files)
	return files
}

// ReadShortcuts merges every user's shortcuts by appid, dropping hidden ones.
// A malformed file is skipped; its error is returned without the path.
func ReadShortcuts(steamRoot string) ([]Shortcut, error) {
	byID := map[uint32]Shortcut{}
	var firstErr error
	for _, f := range ShortcutFiles(steamRoot) {
		if isForbidden(f) || filepath.Base(f) != "shortcuts.vdf" {
			continue
		}
		st, err := os.Stat(f)
		if err != nil || st.Size() > maxShortcutsBytes {
			if firstErr == nil {
				firstErr = errShortcuts
			}
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		list, err := ParseShortcuts(b)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, s := range list {
			if s.Hidden {
				delete(byID, s.AppID)
				continue
			}
			if prev, ok := byID[s.AppID]; !ok || s.LastPlayed.After(prev.LastPlayed) {
				byID[s.AppID] = s
			}
		}
	}
	out := make([]Shortcut, 0, len(byID))
	for _, s := range byID {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AppID < out[j].AppID })
	return out, firstErr
}

// shortcutGameType is the GameID type byte for a non-Steam shortcut.
const shortcutGameType = 0x02

// NormalizeID maps any of the three observed ID forms to the canonical u32 key
// (ADR-0008 D3): a signed int32 (shortcuts.vdf, "Remove … from running list"),
// an unsigned 32-bit id (reaper AppId=, SteamAppId), or a 64-bit GameID
// (SteamGameId, "AppID … adding PID"). Unknown GameID types are rejected.
func NormalizeID(v int64, u uint64, signed bool) (uint32, bool) {
	if signed {
		if v < -1<<31 || v > 1<<32-1 || v == 0 {
			return 0, false
		}
		return uint32(v), true // two's complement: -50861101 -> 4244106195
	}
	if u == 0 {
		return 0, false
	}
	if u <= 1<<32-1 {
		return uint32(u), true
	}
	switch (u >> 24) & 0xFF {
	case shortcutGameType:
		if u&0xFFFFFF != 0 {
			return 0, false
		}
		return uint32(u >> 32), true
	case 0: // Steam game GameID: the appid lives in the low 24 bits
		return uint32(u & 0xFFFFFF), u&0xFFFFFF != 0
	}
	return 0, false
}

// ParseID normalizes a decimal ID string in any of the three forms.
func ParseID(s string) (uint32, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 20 {
		return 0, false
	}
	neg := strings.HasPrefix(s, "-")
	digits := strings.TrimPrefix(s, "-")
	if digits == "" {
		return 0, false
	}
	var u uint64
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}
		d := uint64(c - '0')
		if u > (1<<64-1-d)/10 {
			return 0, false
		}
		u = u*10 + d
	}
	if neg {
		if u > 1<<31 {
			return 0, false
		}
		return NormalizeID(-int64(u), 0, true)
	}
	return NormalizeID(0, u, false)
}

// ShortcutGameID returns the 64-bit GameID for a shortcut's u32 key as a
// decimal string (it exceeds 2^53, so it never travels as a JSON number).
func ShortcutGameID(appID uint32) string {
	return strconv.FormatUint(uint64(appID)<<32|shortcutGameType<<24, 10)
}
