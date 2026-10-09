// gentestvectors — emits canonical cross-language test vectors for the
// capability token spec. Both the Go and Python test suites read the
// output and must verify every vector.
//
// Usage (from repo root):
//
//     go run ./origin/cmd/gentestvectors > docs/test-vectors/capability.json
//
// Checked into the repo so test runs don't need to regenerate.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Tusm11/keystone/origin/internal/primitives/capability"
)

type Vector struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	PublicKeyBase64   string `json:"public_key_b64"`
	PrivateKeyBase64  string `json:"private_key_b64"`
	Capability        struct {
		Code   string `json:"code"`
		Scope  string `json:"scope"`
		Signer string `json:"signer"`
		Exp    int64  `json:"exp"`
		Uses   int64  `json:"uses"`
		Nonce  string `json:"nonce"`
	} `json:"capability"`
	ExpectedToken      string `json:"expected_token"`
	ExpectedBodyBase64 string `json:"expected_body_b64"`
}

type Bundle struct {
	SpecVersion string   `json:"spec_version"`
	Generated   string   `json:"generated"`
	Vectors     []Vector `json:"vectors"`
}

func main() {
	// Deterministic key for reproducible vectors — not a real secret.
	// 32 bytes of fixed seed → one specific Ed25519 keypair.
	seed := []byte("keystone-test-vectors-seed-fixed!")[:ed25519.SeedSize]
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)

	bundle := Bundle{
		SpecVersion: "v1",
		Generated:   time.Now().UTC().Format(time.RFC3339),
	}

	// Fixed nonces so canonical JSON output is byte-stable.
	nonce1 := mustNonce("a1b2c3d4e5f60718293a4b5c6d7e8f90")
	nonce2 := mustNonce("ffeeddccbbaa998877665544332211aa")
	nonce3 := mustNonce("00112233445566778899aabbccddeeff")

	bundle.Vectors = append(bundle.Vectors, build(priv, pub,
		"basic_no_expiry",
		"minimum fields; no expiry, unlimited uses",
		capability.Capability{
			Code: "aB3xZ9k", Scope: "read-only", Signer: "keystone-test",
			Exp: 0, Uses: 0, Nonce: nonce1,
		}))

	bundle.Vectors = append(bundle.Vectors, build(priv, pub,
		"bounded_expiry_and_uses",
		"both exp and uses set; typical production shape",
		capability.Capability{
			Code: "QqRrSsT", Scope: "read-only", Signer: "keystone-test",
			Exp: 1760000000, Uses: 5, Nonce: nonce2,
		}))

	bundle.Vectors = append(bundle.Vectors, build(priv, pub,
		"different_scope",
		"custom scope label; validates scope round-trip",
		capability.Capability{
			Code: "zY1xWvU", Scope: "write-once", Signer: "keystone-test",
			Exp: 1800000000, Uses: 1, Nonce: nonce3,
		}))

	out, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(out)
	_, _ = os.Stdout.Write([]byte("\n"))
}

func build(priv ed25519.PrivateKey, pub ed25519.PublicKey, name, desc string, cap capability.Capability) Vector {
	token, err := cap.Sign(priv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign vector %s: %v\n", name, err)
		os.Exit(1)
	}
	// Extract body_b64 (second part of token) for Python to compare against.
	bodyB64 := ""
	for i, c := range token {
		if c == '.' {
			rest := token[i+1:]
			for j, c2 := range rest {
				if c2 == '.' {
					bodyB64 = rest[:j]
					break
				}
			}
			break
		}
	}

	v := Vector{
		Name:               name,
		Description:        desc,
		PublicKeyBase64:    base64.RawURLEncoding.EncodeToString(pub),
		PrivateKeyBase64:   base64.RawURLEncoding.EncodeToString(priv),
		ExpectedToken:      token,
		ExpectedBodyBase64: bodyB64,
	}
	v.Capability.Code = cap.Code
	v.Capability.Scope = cap.Scope
	v.Capability.Signer = cap.Signer
	v.Capability.Exp = cap.Exp
	v.Capability.Uses = cap.Uses
	v.Capability.Nonce = cap.Nonce
	return v
}

func mustNonce(hexStr string) string {
	// Validate — nonces are hex-encoded 16-byte values (32 hex chars).
	if len(hexStr) != 32 {
		panic("nonce must be 32 hex chars")
	}
	if _, err := hex.DecodeString(hexStr); err != nil {
		panic(err)
	}
	_ = rand.Reader // silence unused import warning
	return hexStr
}
