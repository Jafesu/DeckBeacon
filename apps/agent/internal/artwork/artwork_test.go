package artwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/jafesu/deckbeacon/apps/agent/internal/dispatch"
)

func noise(w, h int, alpha bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(1))
	for i := range img.Pix {
		img.Pix[i] = byte(r.Intn(256))
		if alpha && i%4 == 3 {
			img.Pix[i] = byte(i % 200)
		} else if i%4 == 3 {
			img.Pix[i] = 0xff
		}
	}
	return img
}

func writeJPEG(t *testing.T, path string, w, h int, comment string) {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, noise(w, h, false), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	if comment != "" { // COM segment right after SOI: metadata the agent must drop
		seg := append([]byte{0xFF, 0xFE, 0, 0}, comment...)
		binary.BigEndian.PutUint16(seg[2:], uint16(len(comment)+2))
		data = append(append([]byte{0xFF, 0xD8}, seg...), data[2:]...)
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	var b bytes.Buffer
	_ = png.Encode(&b, noise(w, h, true))
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, b.Bytes(), 0o644)
}

const sha = "0123456789abcdef0123456789abcdef01234567"

func fixture(t *testing.T) string {
	root := t.TempDir()
	lc := filepath.Join(root, "appcache", "librarycache")
	// Flat layout (413150 on the G15).
	writeJPEG(t, filepath.Join(lc, "413150", "library_600x900.jpg"), 600, 900, "CANARY-EXIF-GPS")
	writeJPEG(t, filepath.Join(lc, "413150", "library_hero.jpg"), 1600, 517, "")
	// Hashed layout (1091500).
	writeJPEG(t, filepath.Join(lc, "1091500", sha, "library_capsule.jpg"), 300, 450, "")
	writePNG(t, filepath.Join(lc, "1091500", sha, "logo.png"), 1280, 400)
	// Symlink escape attempts.
	secret := filepath.Join(root, "config", "config.vdf")
	_ = os.MkdirAll(filepath.Dir(secret), 0o755)
	_ = os.WriteFile(secret, []byte("CANARY-CREDENTIAL"), 0o600)
	_ = os.MkdirAll(filepath.Join(lc, "204360"), 0o755)
	_ = os.Symlink(secret, filepath.Join(lc, "204360", "library_600x900.jpg"))
	_ = os.Symlink(filepath.Join(root, "config"), filepath.Join(lc, "945360"))
	// Shortcut grid artwork.
	writePNG(t, filepath.Join(root, "userdata", "10000001", "config", "grid", "4244106195p.png"), 600, 900) // noisy alpha: falls back to JPEG
	return root
}

func TestResolveAndReencode(t *testing.T) {
	src := New(fixture(t), func(id uint32) bool { return id == 4244106195 })
	img, err := src.Get(413150, Capsule)
	if err != nil {
		t.Fatal(err)
	}
	if img.MIME != "image/jpeg" || img.Width != 600 || img.Height != 900 || bytes.Contains(img.Data, []byte("CANARY")) {
		t.Fatalf("capsule %s %dx%d canary=%v", img.MIME, img.Width, img.Height, bytes.Contains(img.Data, []byte("CANARY")))
	}
	if sum := sha256.Sum256(img.Data); hex.EncodeToString(sum[:]) != img.SHA256 {
		t.Fatal("sha256 mismatch")
	}
	hero, _ := src.Get(413150, Hero)
	if hero.Width != 1280 || hero.Height != 414 {
		t.Fatalf("hero downscaled to %dx%d, want 1280x414", hero.Width, hero.Height)
	}
	if c, err := src.Get(1091500, Capsule); err != nil || c.Width != 300 {
		t.Fatalf("hashed capsule %v %+v (never upscaled)", err, c)
	}
	logo, err := src.Get(1091500, Logo)
	if err != nil || logo.MIME != "image/png" || logo.Width != 640 || logo.Height != 200 {
		t.Fatalf("logo %v %+v", err, logo)
	}
	if g, err := src.Get(4244106195, Capsule); err != nil || g.Width != 600 {
		t.Fatalf("shortcut grid %v", err)
	}
	for _, c := range []struct {
		id   uint32
		kind Kind
	}{{204360, Capsule}, {945360, Capsule}, {413150, Logo}, {999, Header}, {4244106195, Hero}} {
		if _, err := src.Get(c.id, c.kind); err != ErrNotFound {
			t.Errorf("%d/%s: want ErrNotFound (symlinks are refused), got %v", c.id, c.kind, err)
		}
	}
}

// A PNG header claiming 50000x50000 must be refused before decoding.
func TestDecompressionBombRefused(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "appcache", "librarycache", "1", "library_600x900.jpg")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1)))
	data := b.Bytes()
	ihdr := data[8+8 : 8+8+13] // after signature + length/type
	binary.BigEndian.PutUint32(ihdr[0:], 50000)
	binary.BigEndian.PutUint32(ihdr[4:], 50000)
	binary.BigEndian.PutUint32(data[8+8+13:], crc32.ChecksumIEEE(data[8+4:8+8+13]))
	_ = os.WriteFile(p, data, 0o644)
	if _, err := New(root, nil).Get(1, Capsule); err == nil {
		t.Fatal("oversized image must be refused")
	}
}

func TestHandlerChunksReassemble(t *testing.T) {
	root := t.TempDir()
	// Noise compresses badly: a large hero yields several chunks.
	writeJPEG(t, filepath.Join(root, "appcache", "librarycache", "7", "library_hero.jpg"), 1920, 1080, "")
	h := New(root, nil).Handler()
	r, follow := h(context.Background(), dispatch.Command{CommandID: "01JDAAAAAAAAAAAAAAAAAAAR1A", Name: "get_artwork", Args: json.RawMessage(`{"app_id":7,"kind":"hero"}`)})
	if r.State != "confirmed" || follow != nil || len(r.Attachments) < 2 {
		t.Fatalf("result %+v (%d chunks)", r.State, len(r.Attachments))
	}
	var all []byte
	for i, a := range r.Attachments {
		body := a.Body.(map[string]any)
		if a.Type != "artwork" || body["seq"] != i || body["total"] != len(r.Attachments) {
			t.Fatalf("chunk %d: %+v", i, body["seq"])
		}
		d, _ := base64.StdEncoding.DecodeString(body["data"].(string))
		if len(d) > ChunkBytes {
			t.Fatalf("chunk %d is %d bytes", i, len(d))
		}
		all = append(all, d...)
	}
	sum := sha256.Sum256(all)
	if hex.EncodeToString(sum[:]) != r.Result["sha256"] || len(all) > maxOutput {
		t.Fatal("reassembled image does not match")
	}
	if _, err := jpeg.Decode(bytes.NewReader(all)); err != nil {
		t.Fatal(err)
	}
	for args, code := range map[string]string{`{"app_id":7,"kind":"../x"}`: "invalid_args", `{"kind":"hero"}`: "invalid_args", `{"app_id":8,"kind":"hero"}`: "unavailable"} {
		r, _ := h(context.Background(), dispatch.Command{CommandID: "C", Args: json.RawMessage(args)})
		if r.State != "rejected" || r.Error.Code != code {
			t.Errorf("%s: %+v", args, r)
		}
	}
	_ = color.Black
}
