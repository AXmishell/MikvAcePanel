// Package idgen provides random identifiers used by users, nodes and
// sessions. It has no persistence dependencies so application services can
// generate IDs without importing the store.
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Token returns a URL-safe random hex token with the requested number of
// random bytes.
func Token(n int) string {
	if n <= 0 {
		n = 16
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is fatal for security tokens; fall back to a
		// time based value rather than producing an empty token.
		return fmt.Sprintf("%x%x", time.Now().UnixNano(), b)
	}
	return hex.EncodeToString(b)
}

// UUID returns a random RFC 4122 version 4 UUID string.
func UUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return Token(16)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
