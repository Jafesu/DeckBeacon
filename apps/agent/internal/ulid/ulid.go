// Package ulid generates ULIDs (48-bit ms timestamp + 80 random bits, Crockford base32).
package ulid

import (
	"crypto/rand"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// New returns a ULID for the current time.
func New() string { return At(time.Now()) }

// At returns a ULID for t with fresh randomness.
func At(t time.Time) string {
	var b [16]byte
	ms := uint64(t.UnixMilli())
	for i := 5; i >= 0; i-- {
		b[i] = byte(ms)
		ms >>= 8
	}
	if _, err := rand.Read(b[6:]); err != nil {
		panic(err) // crypto/rand never fails on Linux
	}
	return encode(b)
}

func encode(b [16]byte) string {
	// 128 bits -> 26 chars; first char carries the top 3 bits (value <= 7).
	out := make([]byte, 26)
	var acc uint64
	bits := 0
	// Process as a big integer from the most significant end: prepend 2 zero bits.
	in := append([]byte{0}, b[:]...) // 136 bits; skip the first 6 to get 130 = 26*5
	skip := 6
	idx := 0
	for _, by := range in {
		for i := 7; i >= 0; i-- {
			if skip > 0 {
				skip--
				continue
			}
			acc = acc<<1 | uint64((by>>uint(i))&1)
			bits++
			if bits == 5 {
				out[idx] = alphabet[acc]
				idx++
				acc, bits = 0, 0
			}
		}
	}
	return string(out)
}
