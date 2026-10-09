// Package signing — key loading + signer registry.
//
// A Keystone deployment has one or more signers, each identified by an
// opaque string in the capability token's "signer" field. A verifier
// looks the signer id up here and uses the matching public key.
//
// v1 ships a single-signer registry built from env at boot:
//
//     KEYSTONE_SIGNING_PRIVATE_KEY — base64url-no-pad Ed25519 private key
//     KEYSTONE_SIGNER_ID           — opaque label, embedded in tokens
//
// Multi-signer, rotation, and public-only verifier mode come later —
// the Registry interface already supports them.
package signing

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

var (
	ErrNoSigner      = errors.New("signing: no local signer configured")
	ErrUnknownSigner = errors.New("signing: signer id not registered")
)

// Registry maps a signer id → its public key for verifier lookup, and
// optionally holds a private key the origin signs with.
type Registry struct {
	publicKeys map[string]ed25519.PublicKey

	// Local signer: optional. Set when this process can mint tokens.
	localID         string
	localPrivateKey ed25519.PrivateKey
}

// NewFromEnv loads the local signer from env, if present. A verifier-
// only deployment (no private key) is a valid configuration.
func NewFromEnv() (*Registry, error) {
	r := &Registry{publicKeys: make(map[string]ed25519.PublicKey)}

	privB64 := os.Getenv("KEYSTONE_SIGNING_PRIVATE_KEY")
	signerID := os.Getenv("KEYSTONE_SIGNER_ID")
	if privB64 == "" {
		return r, nil // verifier-only; capabilities disabled on this node
	}
	if signerID == "" {
		return nil, errors.New("signing: KEYSTONE_SIGNING_PRIVATE_KEY set but KEYSTONE_SIGNER_ID missing")
	}

	privBytes, err := base64.RawURLEncoding.DecodeString(privB64)
	if err != nil {
		return nil, fmt.Errorf("signing: decode private key: %w", err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("signing: private key is %d bytes, want %d", len(privBytes), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(privBytes)
	pub := priv.Public().(ed25519.PublicKey)

	r.localID = signerID
	r.localPrivateKey = priv
	r.publicKeys[signerID] = pub
	return r, nil
}

// HasLocalSigner reports whether this node can mint tokens.
func (r *Registry) HasLocalSigner() bool {
	return r.localPrivateKey != nil
}

// LocalSigner returns the local signer id and private key, or ErrNoSigner.
func (r *Registry) LocalSigner() (string, ed25519.PrivateKey, error) {
	if r.localPrivateKey == nil {
		return "", nil, ErrNoSigner
	}
	return r.localID, r.localPrivateKey, nil
}

// PublicKey looks up a public key by signer id for verification.
func (r *Registry) PublicKey(signerID string) (ed25519.PublicKey, error) {
	pk, ok := r.publicKeys[signerID]
	if !ok {
		return nil, ErrUnknownSigner
	}
	return pk, nil
}
