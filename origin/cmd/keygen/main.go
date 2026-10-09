// keygen: generate an Ed25519 keypair for Keystone signing.
//
// Usage:
//   go run ./cmd/keygen
//
// Prints base64url-encoded private + public key to stdout. The private
// key stays on the signer (origin); the public key goes to every verifier
// (edge, Python package consumers). Treat the private key like any secret
// — don't check it into git.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

func main() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("KEYSTONE_SIGNING_PRIVATE_KEY=%s\n", base64.RawURLEncoding.EncodeToString(priv))
	fmt.Printf("KEYSTONE_SIGNING_PUBLIC_KEY=%s\n", base64.RawURLEncoding.EncodeToString(pub))
	fmt.Fprintln(os.Stderr, "\nAdd the first line to your .env (private key — secret).")
	fmt.Fprintln(os.Stderr, "Distribute the public key to every verifier.")
}
