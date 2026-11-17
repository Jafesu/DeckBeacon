// Package artwork serves game artwork from Steam's local caches to Home
// Assistant (M8, ADR-0009). It never forwards file bytes: images are found
// under strict path rules, decoded, downscaled and re-encoded, so HA only ever
// receives freshly encoded pixels with no metadata.
package artwork

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"

	"golang.org/x/image/draw"
)

// Kind is an artwork slot.
type Kind string

const (
	Capsule Kind = "capsule" // portrait library capsule
	Header  Kind = "header"  // wide store header
	Hero    Kind = "hero"    // library hero banner
	Logo    Kind = "logo"    // transparent title logo
)

// ParseKind validates a kind from a command (an allowlist, never a file name).
func ParseKind(s string) (Kind, bool) {
	switch k := Kind(s); k {
	case Capsule, Header, Hero, Logo:
		return k, true
	}
	return "", false
}

// Limits (ADR-0009).
const (
	maxInputBytes = 32 << 20   // G15: largest logo.png is 16.2 MB; the pixel guard is the real limit
	maxPixels     = 40_000_000 // decompression-bomb guard, checked before decoding
	maxOutput     = 512 << 10
	ChunkBytes    = 32 << 10 // binary per artwork message (Noise plaintext limit)
)

var maxSize = map[Kind][2]int{Capsule: {600, 900}, Header: {920, 430}, Hero: {1280, 1280}, Logo: {640, 640}}

// Steam librarycache names per kind, flat in <appid>/ or one level down in
// <appid>/<sha1>/ (G15 §6 and the M8 artwork survey: 3,038 appids, both
// layouts, sometimes mixed). Names don't promise sizes: 949 of the G15's
// library_600x900.jpg files are 300x450, so every candidate is measured and
// the largest wins.
var (
	names = map[Kind][]string{
		Capsule: {"library_600x900.jpg", "library_capsule.jpg"},
		Header:  {"header.jpg", "library_header.jpg"},
		Hero:    {"library_hero.jpg"},
		Logo:    {"logo.png"},
	}
	hashedDir = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Image is a re-encoded artwork.
type Image struct {
	Data          []byte
	MIME          string
	Width, Height int
	SHA256        string
}

// ErrNotFound means no artwork of that kind exists for the app.
var ErrNotFound = errors.New("no artwork")

// Source resolves artwork for apps under one Steam installation.
type Source struct {
	steamRoot string
	shortcut  func(appID uint32) bool
	mu        sync.Mutex
	cache     map[string]*Image
	order     []string
	cacheSize int
}

// New creates a source. shortcut reports whether an app id is a non-Steam
// shortcut (its artwork lives in the user's grid folder, not librarycache).
func New(steamRoot string, shortcut func(uint32) bool) *Source {
	if shortcut == nil {
		shortcut = func(uint32) bool { return false }
	}
	return &Source{steamRoot: steamRoot, shortcut: shortcut, cache: map[string]*Image{}}
}

// Get returns the re-encoded artwork, cached by (app, kind, file, mtime, size).
func (s *Source) Get(appID uint32, kind Kind) (*Image, error) {
	path, st, err := s.resolve(appID, kind)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%d|%s|%s|%d|%d", appID, kind, path, st.ModTime().UnixNano(), st.Size())
	s.mu.Lock()
	if img, ok := s.cache[key]; ok {
		s.mu.Unlock()
		return img, nil
	}
	s.mu.Unlock()
	img, err := encode(path, kind)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[key] = img
	s.order = append(s.order, key)
	s.cacheSize += len(img.Data)
	for s.cacheSize > 32<<20 && len(s.order) > 1 { // bounded memory
		old := s.order[0]
		s.order = s.order[1:]
		if o, ok := s.cache[old]; ok {
			s.cacheSize -= len(o.Data)
			delete(s.cache, old)
		}
	}
	return img, nil
}

// regularFile checks a path with Lstat: a regular file (no symlink), size-capped.
func regularFile(path string) (os.FileInfo, bool) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > maxInputBytes {
		return nil, false
	}
	return st, true
}

// realDir checks that path is a directory and not a symlink.
func realDir(path string) bool {
	st, err := os.Lstat(path)
	return err == nil && st.IsDir()
}

func (s *Source) resolve(appID uint32, kind Kind) (string, os.FileInfo, error) {
	if s.steamRoot == "" || appID == 0 {
		return "", nil, ErrNotFound
	}
	if s.shortcut(appID) {
		return s.resolveGrid(appID, kind)
	}
	base := filepath.Join(s.steamRoot, "appcache", "librarycache")
	dir := filepath.Join(base, strconv.FormatUint(uint64(appID), 10))
	if !realDir(filepath.Join(s.steamRoot, "appcache")) || !realDir(base) || !realDir(dir) {
		return "", nil, ErrNotFound
	}
	dirs := []string{dir}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if hashedDir.MatchString(e.Name()) && realDir(filepath.Join(dir, e.Name())) {
			dirs = append(dirs, filepath.Join(dir, e.Name()))
		}
	}
	var best string
	var bestSt os.FileInfo
	bestArea := -1
	for _, d := range dirs {
		for _, name := range names[kind] {
			p := filepath.Join(d, name)
			st, ok := regularFile(p)
			if !ok {
				continue
			}
			area := measure(p)
			if area > bestArea || (area == bestArea && st.ModTime().After(bestSt.ModTime())) {
				best, bestSt, bestArea = p, st, area
			}
		}
	}
	if bestSt == nil || bestArea <= 0 {
		return "", nil, ErrNotFound
	}
	return best, bestSt, nil
}

// measure reads only the image header (format sniffed from the bytes, not the
// extension: the G15 has a JPEG named *.png) and returns the pixel area.
func measure(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil || (format != "jpeg" && format != "png") {
		return 0
	}
	return cfg.Width * cfg.Height
}

// gridSuffix is Steam's custom-artwork naming in userdata/*/config/grid/.
var gridSuffix = map[Kind]string{Capsule: "p", Header: "", Hero: "_hero", Logo: "_logo"}

func (s *Source) resolveGrid(appID uint32, kind Kind) (string, os.FileInfo, error) {
	userdata := filepath.Join(s.steamRoot, "userdata")
	if !realDir(userdata) {
		return "", nil, ErrNotFound
	}
	users, _ := os.ReadDir(userdata)
	name := strconv.FormatUint(uint64(appID), 10) + gridSuffix[kind]
	for _, u := range users {
		if _, err := strconv.ParseUint(u.Name(), 10, 64); err != nil {
			continue
		}
		grid := filepath.Join(userdata, u.Name(), "config", "grid")
		if !realDir(filepath.Join(userdata, u.Name())) || !realDir(filepath.Join(userdata, u.Name(), "config")) || !realDir(grid) {
			continue
		}
		for _, ext := range []string{".png", ".jpg", ".jpeg"} {
			p := filepath.Join(grid, name+ext)
			if st, ok := regularFile(p); ok {
				return p, st, nil
			}
		}
	}
	return "", nil, ErrNotFound
}

func encode(path string, kind Kind) (*Image, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrNotFound
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, fmt.Errorf("artwork: unsupported image")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("artwork: image too large")
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("artwork: undecodable image")
	}
	limit := maxSize[kind]
	w, h := fit(cfg.Width, cfg.Height, limit[0], limit[1])
	var out bytes.Buffer
	mime := ""
	for attempt := 0; attempt < 5; attempt++ {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
		if kind == Logo || (format == "png" && !opaque(dst)) {
			out.Reset()
			if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, dst); err != nil {
				return nil, err
			}
			if out.Len() <= maxOutput {
				mime = "image/png"
				break
			}
			if kind == Logo { // keep transparency: shrink until it fits
				w, h = max(1, w*7/10), max(1, h*7/10)
				continue
			}
			// Other kinds: flatten (premultiplied RGBA over black) and use JPEG.
		}
		for q := 82; ; q -= 12 {
			out.Reset()
			if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: q}); err != nil {
				return nil, err
			}
			if out.Len() <= maxOutput || q <= 46 {
				break
			}
		}
		if out.Len() <= maxOutput {
			mime = "image/jpeg"
			break
		}
		w, h = max(1, w*7/10), max(1, h*7/10)
	}
	if mime == "" {
		return nil, fmt.Errorf("artwork: encoded image too large")
	}
	sum := sha256.Sum256(out.Bytes())
	return &Image{Data: out.Bytes(), MIME: mime, Width: w, Height: h, SHA256: hex.EncodeToString(sum[:])}, nil
}

// fit scales (w,h) down to fit (mw,mh), keeping the aspect ratio; never up.
func fit(w, h, mw, mh int) (int, int) {
	if w <= mw && h <= mh {
		return w, h
	}
	r := min(float64(mw)/float64(w), float64(mh)/float64(h))
	return max(1, int(float64(w)*r+0.5)), max(1, int(float64(h)*r+0.5))
}

func opaque(img *image.RGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0xff {
			return false
		}
	}
	return true
}

// Chunks splits the image for artwork messages.
func (img *Image) Chunks() [][]byte {
	var out [][]byte
	for i := 0; i < len(img.Data); i += ChunkBytes {
		out = append(out, img.Data[i:min(len(img.Data), i+ChunkBytes)])
	}
	return out
}
