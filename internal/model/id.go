package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// NewID returns a prefixed random identifier such as "conv_a1b2c3d4e5f60718".
func NewID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return prefix + "_" + hex.EncodeToString(b)
}

// HashDedupeKey derives a stable dedupe key when no provider message ID exists,
// from provider + conversation + sender + timestamp + body (spec §5).
func HashDedupeKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return "hash:" + hex.EncodeToString(h.Sum(nil))[:32]
}
