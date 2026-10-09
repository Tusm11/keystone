package capability

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"
)

func newKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

func TestRoundTrip(t *testing.T) {
	pub, priv := newKeypair(t)

	cap, err := New("aB3xZ9k", "read-only", "test-signer")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cap.Exp = time.Now().Add(time.Hour).Unix()
	cap.Uses = 5

	token, err := cap.Sign(priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !strings.HasPrefix(token, "v1.") {
		t.Errorf("token missing v1 prefix: %q", token)
	}
	if parts := strings.Split(token, "."); len(parts) != 3 {
		t.Errorf("token has %d parts, want 3", len(parts))
	}

	got, err := Verify(token, pub, time.Now())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Code != "aB3xZ9k" || got.Scope != "read-only" || got.Signer != "test-signer" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.Uses != 5 {
		t.Errorf("Uses mismatch: got %d, want 5", got.Uses)
	}
}

func TestVerify_BadSignature(t *testing.T) {
	pub, priv := newKeypair(t)
	cap, _ := New("abc", "read-only", "s")
	token, _ := cap.Sign(priv)

	// Tamper with the body. Must fail signature check.
	parts := strings.Split(token, ".")
	tampered := parts[0] + "." + strings.ToUpper(parts[1]) + "." + parts[2]
	if _, err := Verify(tampered, pub, time.Now()); err == nil {
		t.Error("tampered token verified; expected ErrBadSignature or ErrMalformed")
	}
}

func TestVerify_WrongKey(t *testing.T) {
	_, priv := newKeypair(t)
	otherPub, _ := newKeypair(t)
	cap, _ := New("abc", "read-only", "s")
	token, _ := cap.Sign(priv)

	if _, err := Verify(token, otherPub, time.Now()); err != ErrBadSignature {
		t.Errorf("wrong-key verify got %v, want ErrBadSignature", err)
	}
}

func TestVerify_Expired(t *testing.T) {
	pub, priv := newKeypair(t)
	cap, _ := New("abc", "read-only", "s")
	cap.Exp = time.Now().Add(-1 * time.Minute).Unix() // 1 min ago
	token, _ := cap.Sign(priv)

	if _, err := Verify(token, pub, time.Now()); err != ErrExpired {
		t.Errorf("expired verify got %v, want ErrExpired", err)
	}
	// Verify succeeds when `now` is zero (skip expiry check).
	if _, err := Verify(token, pub, time.Time{}); err != nil {
		t.Errorf("zero-time verify failed: %v", err)
	}
}

func TestVerify_Malformed(t *testing.T) {
	pub, _ := newKeypair(t)
	cases := []string{
		"",
		"v1.body",             // only 2 parts
		"v1.body.sig.extra",   // 4 parts
		"v2.body.sig",         // bad version
		"v1.!!!.sig",          // invalid base64
	}
	for _, c := range cases {
		if _, err := Verify(c, pub, time.Now()); err == nil {
			t.Errorf("malformed %q verified unexpectedly", c)
		}
	}
}

func TestNonceUniqueness(t *testing.T) {
	// Two capabilities for the same code must get different nonces, so
	// their tokens differ even with identical policy. This prevents
	// trivial token-reuse across issuances.
	cap1, _ := New("abc", "read-only", "s")
	cap2, _ := New("abc", "read-only", "s")
	if cap1.Nonce == cap2.Nonce {
		t.Error("two fresh capabilities produced the same nonce")
	}
}

func TestCanonicalJSONStability(t *testing.T) {
	// Two capabilities with identical field values produce identical body
	// bytes under canonical JSON — the property Python port depends on.
	cap1 := &Capability{Code: "x", Scope: "r", Exp: 100, Uses: 1, Signer: "s", Nonce: "aa"}
	cap2 := &Capability{Code: "x", Scope: "r", Exp: 100, Uses: 1, Signer: "s", Nonce: "aa"}
	b1, err := canonicalJSON(cap1)
	if err != nil {
		t.Fatalf("canonicalJSON cap1: %v", err)
	}
	b2, err := canonicalJSON(cap2)
	if err != nil {
		t.Fatalf("canonicalJSON cap2: %v", err)
	}
	if string(b1) != string(b2) {
		t.Errorf("canonical JSON unstable:\n  %s\n  %s", b1, b2)
	}
	// Keys must be sorted. Expected first key lex-sorted is "code".
	if !strings.HasPrefix(string(b1), `{"code":`) {
		t.Errorf("canonical JSON doesn't start with sorted first key: %s", b1)
	}
}
