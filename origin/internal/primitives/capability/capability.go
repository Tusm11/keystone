// Package capability — signed capability tokens for short links.
//
// A capability says "the holder of this token may redirect code X under
// scope Y, until expiry Z, N more times." It is independent of the code
// itself: codes stay the plain base62 strings; a capability is an extra
// signed envelope the client presents at resolve time.
//
// This package is pure. No I/O, no state, no logging. The ONLY runtime
// dependency is Go's standard library crypto/ed25519. That's deliberate:
// this module ports mechanically to Python as keystone-core, and the
// signature it produces must verify in both languages against the same
// public key. The wire format is specified in docs/capability-spec.md
// and the test suite exercises known-vector round-trips so a Python port
// can be validated byte-for-byte.
//
// Wire format (reference — authoritative spec in docs/capability-spec.md):
//
//     token    := "v1." + body_b64 + "." + sig_b64
//     body     := canonical JSON of:
//                 { "code":<str>, "scope":<str>, "exp":<int unix seconds>,
//                   "uses":<int, 0=unlimited>, "signer":<str>, "nonce":<hex 16> }
//     body_b64 := base64url(body) without padding
//     sig_b64  := base64url(Ed25519 sign of ("v1." + body_b64)) without padding
//
// Canonical JSON: keys sorted lexicographically, no insignificant
// whitespace, UTF-8. This is what makes Python↔Go interop reliable.
package capability

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const Version = "v1"

var (
	ErrMalformed    = errors.New("capability: malformed token")
	ErrBadVersion   = errors.New("capability: unsupported version")
	ErrBadSignature = errors.New("capability: signature invalid")
	ErrExpired      = errors.New("capability: expired")
	ErrScopeMissing = errors.New("capability: scope empty")
	ErrCodeMissing  = errors.New("capability: code empty")
)

// Capability is the decoded body of a token. Field order here does NOT
// decide wire order — canonical JSON sorts keys lexicographically.
type Capability struct {
	Code     string    `json:"code"`
	Scope    string    `json:"scope"`
	Exp      int64     `json:"exp"`     // Unix seconds; 0 = no expiry
	Uses     int64     `json:"uses"`    // 0 = unlimited
	Signer   string    `json:"signer"`
	Nonce    string    `json:"nonce"`   // hex-encoded, 16 bytes, prevents replay-across-code
}

// ExpiresAt returns the expiry as a time.Time (zero if no expiry set).
func (c *Capability) ExpiresAt() time.Time {
	if c.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(c.Exp, 0).UTC()
}

// New builds an unsigned Capability with a fresh nonce. Caller fills in
// the policy fields, then calls Sign.
func New(code, scope, signer string) (*Capability, error) {
	if code == "" {
		return nil, ErrCodeMissing
	}
	if scope == "" {
		return nil, ErrScopeMissing
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return &Capability{
		Code:   code,
		Scope:  scope,
		Signer: signer,
		Nonce:  hex.EncodeToString(nonce),
	}, nil
}

// Sign serializes the capability to canonical JSON and returns a v1 token.
// The key MUST be the Ed25519 PRIVATE key of the signer.
func (c *Capability) Sign(key ed25519.PrivateKey) (string, error) {
	if c.Code == "" {
		return "", ErrCodeMissing
	}
	if c.Scope == "" {
		return "", ErrScopeMissing
	}
	body, err := canonicalJSON(c)
	if err != nil {
		return "", err
	}
	bodyB64 := base64.RawURLEncoding.EncodeToString(body)
	signingInput := Version + "." + bodyB64
	sig := ed25519.Sign(key, []byte(signingInput))
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return signingInput + "." + sigB64, nil
}

// Verify parses the token, checks the signature against publicKey, and
// (if now != zero) rejects expired tokens. On success the decoded
// Capability is returned. This is the only path callers should use to
// treat a token as trusted.
func Verify(token string, publicKey ed25519.PublicKey, now time.Time) (*Capability, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrMalformed
	}
	if parts[0] != Version {
		return nil, ErrBadVersion
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrMalformed
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	if !ed25519.Verify(publicKey, signingInput, sig) {
		return nil, ErrBadSignature
	}
	var cap Capability
	if err := json.Unmarshal(body, &cap); err != nil {
		return nil, ErrMalformed
	}
	if cap.Code == "" {
		return nil, ErrCodeMissing
	}
	if cap.Scope == "" {
		return nil, ErrScopeMissing
	}
	if !now.IsZero() && cap.Exp != 0 && now.Unix() > cap.Exp {
		return nil, ErrExpired
	}
	return &cap, nil
}

// canonicalJSON encodes v with keys sorted ascending and no insignificant
// whitespace. Go's encoding/json already sorts struct fields in struct-
// declaration order, which is NOT lexicographic — so we marshal, decode
// into a map, and re-marshal with a sorted-keys encoder. For a tiny
// struct this is fine; for hot-path code we'd hand-roll.
func canonicalJSON(v any) ([]byte, error) {
	first, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(first, &m); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// json.Encoder on a map writes keys sorted ascending — the exact
	// guarantee we need for canonical form.
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	// Encoder appends a trailing newline; strip it so the signed input
	// is exactly the JSON object.
	out := buf.Bytes()
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out, nil
}
