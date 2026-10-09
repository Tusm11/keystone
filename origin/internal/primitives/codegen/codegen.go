// Package codegen generates short URL codes.
// Pure functions only — no I/O, no state. Portable to any language.
// This package is the first chunk that will eventually extract into the
// Python keystone-core package, so it stays dependency-free on purpose.
package codegen

import (
	"crypto/rand"
	"math/big"
)

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// Random generates a cryptographically random base62 code of the given length.
// Length 7 gives 62^7 = ~3.5 trillion combinations; collisions are rare but
// the caller should still retry on ErrCodeTaken from the store.
func Random(length int) (string, error) {
	if length <= 0 {
		length = 7
	}
	out := make([]byte, length)
	max := big.NewInt(int64(len(alphabet)))
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out), nil
}
