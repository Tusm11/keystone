# keystone-core

Signed capability tokens and short-code primitives for
[Keystone](https://github.com/Tusm11/keystone) — the Python port of the
v1 wire-format spec.

A capability token is a short, bearer, cryptographically signed envelope
authorizing a specific short link under a specific scope, with optional
expiry and use-count. The Keystone origin (Go) mints them; any verifier
in any language can validate them against the public key — this
package is the Python verifier/signer.

## Install

```
pip install keystone-core
```

Requires Python 3.9+ and `cryptography>=42` (for Ed25519).

## Usage

```python
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from keystone_core import Capability, verify_capability, random_code

priv = Ed25519PrivateKey.generate()
pub = priv.public_key()

# Mint
cap = Capability(code="aB3xZ9k", scope="read-only", signer="my-service",
                 exp=0, uses=5)   # exp=0 → no expiry; uses=5 → bearer may redeem 5 times
token = cap.sign(priv)
print(token)
# → "v1.<base64url body>.<base64url signature>"

# Verify (in another process, another language, however you got the token)
back = verify_capability(token, pub)
print(back.code, back.scope, back.uses)
# → "aB3xZ9k" "read-only" 5

# Generate a plain short code (no signing)
print(random_code())    # "aB3xZ9k"
print(random_code(10))  # "xY2fK9mNbA"
```

Verification raises specific exceptions on failure:
`MalformedError`, `BadVersionError`, `BadSignatureError`, `ExpiredError`,
`EmptyCodeError`, `EmptyScopeError`.

## Why this exists

Every short-link redirect in Keystone can carry an optional `?k=<token>`
that gates the redirect behind a signed capability — "the holder of this
token may follow code X, until Y, at most N times." The Go origin signs
them with its private Ed25519 key. **Any client**, in any language, can
verify them without round-tripping to the origin, given only the public
key.

The wire format is small, deterministic, and language-independent:

```
token    = "v1." + body_b64 + "." + sig_b64
body     = canonical JSON of {code, exp, nonce, scope, signer, uses}
          (keys sorted, no whitespace)
body_b64 = base64url-no-pad(body)
sig_b64  = base64url-no-pad(Ed25519-sign("v1." + body_b64))
```

The authoritative spec is [capability-spec.md](https://github.com/Tusm11/keystone/blob/master/docs/capability-spec.md).

## Cross-language guarantee

This package is tested against vectors produced by the Go reference
implementation. Every test run re-signs each vector with Python and
asserts the result is byte-identical to the Go-signed token. If
canonical JSON, base64 encoding, or the signing input construction ever
drifts, the test fails loudly. See `tests/test_cross_language.py`.

## Status

Alpha, v0.1.0. API may change until v1.0.0. If you build on this and
hit a wart, open an issue.

## License

Apache-2.0.
