// Package ids makes the identifiers and secrets Aboard hands out: sortable ids for
// records, bearer tokens, and short join codes. Randomness comes from a reader the caller
// supplies, so tests can make them repeatable.
package ids

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"
)

// crockford is Crockford's base32 alphabet: no I, L, O or U, so codes read aloud cleanly.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Generator makes ids, tokens and join codes from one source of randomness.
type Generator struct {
	rand io.Reader
}

// New returns a Generator reading randomness from r (crypto/rand.Reader in production).
func New(r io.Reader) *Generator { return &Generator{rand: r} }

// ID returns prefix + "_" + a 26-character ULID: 48 bits of milliseconds since the Unix
// epoch, then 80 random bits, so ids sort by creation time.
func (g *Generator) ID(prefix string, now time.Time) (string, error) {
	var b [16]byte
	ms := uint64(now.UnixMilli())
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], ms)
	copy(b[:6], ts[2:])
	if _, err := io.ReadFull(g.rand, b[6:]); err != nil {
		return "", fmt.Errorf("read randomness: %w", err)
	}
	return prefix + "_" + encodeULID(b), nil
}

func encodeULID(b [16]byte) string {
	// 128 bits as 26 base32 characters; the first character carries the top 3 bits.
	var out [26]byte
	var acc uint64
	var bits uint
	i := 25
	for j := 15; j >= 0; j-- {
		acc |= uint64(b[j]) << bits
		bits += 8
		for bits >= 5 && i >= 0 {
			out[i] = crockford[acc&31]
			acc >>= 5
			bits -= 5
			i--
		}
	}
	for i >= 0 {
		out[i] = crockford[acc&31]
		acc >>= 5
		i--
	}
	return string(out[:])
}

// Token returns a bearer token: kind prefix ("abh" for humans, "aba" for agents), "_",
// and 32 random bytes in URL-safe base64.
func (g *Generator) Token(kind string) (string, error) {
	var b [32]byte
	if _, err := io.ReadFull(g.rand, b[:]); err != nil {
		return "", fmt.Errorf("read randomness: %w", err)
	}
	return kind + "_" + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// JoinCode returns six Crockford base32 characters written as "7Q4-K2M".
func (g *Generator) JoinCode() (string, error) {
	var b [6]byte
	if _, err := io.ReadFull(g.rand, b[:]); err != nil {
		return "", fmt.Errorf("read randomness: %w", err)
	}
	var out [6]byte
	for i, v := range b {
		out[i] = crockford[v&31]
	}
	return string(out[:3]) + "-" + string(out[3:]), nil
}

// NormalizeJoinCode turns what a person typed into the canonical "XXX-XXX" form. It
// accepts any case, with or without the dash, and reads O as 0 and I or L as 1.
func NormalizeJoinCode(s string) (string, bool) {
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(s)))
	s = strings.NewReplacer("O", "0", "I", "1", "L", "1").Replace(s)
	if len(s) != 6 {
		return "", false
	}
	for _, c := range s {
		if !strings.ContainsRune(crockford, c) {
			return "", false
		}
	}
	return s[:3] + "-" + s[3:], true
}

// Digest returns the keyed SHA-256 digest of a secret. Only digests are stored, so a
// copy of the database doesn't contain working tokens or join codes.
func Digest(key []byte, secret string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(secret))
	return hex.EncodeToString(m.Sum(nil))
}
