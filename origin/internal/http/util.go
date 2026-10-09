package http

import (
	"encoding/base64"
	"strings"
)

// splitN is strings.SplitN against a byte separator — used for the
// three-part capability token parse. Lives here to keep handlers.go
// focused on request handling.
func splitN(s string, sep byte, n int) []string {
	return strings.SplitN(s, string(sep), n)
}

func base64RawURLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
