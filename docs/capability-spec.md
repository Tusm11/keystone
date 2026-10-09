# Keystone Capability Token Spec (v1)

> This is the authoritative wire-format spec. Any implementation — Go,
> Python, Rust, TypeScript — that follows this document produces tokens
> verifiable by any other. Treat this file as the contract; the Go
> implementation in `origin/internal/primitives/capability/` is one
> conformant implementation, not the definition.

## Purpose

A **capability token** is a signed envelope that says:

> *"The holder may redirect short code `C` under scope `S`, until Unix
> second `exp`, no more than `uses` times, signed by `signer`."*

It is independent of the short code itself. A plain short code is public;
a capability token is private, bearer-based, and verifiable without
calling the issuer.

## Token shape

```
token    := "v1" "." body_b64 "." sig_b64
body_b64 := base64url-no-pad( body )
sig_b64  := base64url-no-pad( Ed25519-sign( body_signing_input ) )
body_signing_input := "v1." + body_b64            (UTF-8 bytes)
```

- **Version**: literal `v1`. Future revisions bump this; verifiers MUST
  reject unknown versions.
- **Separators**: three parts joined by ASCII `.`. Exactly two dots.
- **Base64**: URL-safe alphabet (RFC 4648 §5), **no padding** (`=`
  stripped). Both parts.
- **Signature**: Ed25519 (RFC 8032). 64-byte raw signature, base64url
  encoded. 512-bit security, deterministic, side-channel resistant,
  small keys and signatures. The right default in 2026.

## Body (canonical JSON)

The body is a UTF-8 JSON object with these fields:

| Field    | Type   | Required | Meaning                                              |
|----------|--------|----------|------------------------------------------------------|
| `code`   | string | yes      | Short code the capability authorizes.                |
| `scope`  | string | yes      | Opaque policy label. v1 defines `read-only`.         |
| `exp`    | int    | yes      | Unix seconds. `0` = never expires.                   |
| `uses`   | int    | yes      | Max resolutions. `0` = unlimited.                    |
| `signer` | string | yes      | Opaque issuer id. Verifier picks public key by this. |
| `nonce`  | string | yes      | 16 random bytes, lowercase hex (32 chars).           |

### Canonical JSON rules

Reproducing identical body bytes across implementations is **the
property signing depends on**. These rules make it reliable:

1. **UTF-8**, no byte-order mark.
2. **Object keys**: lexicographic ascending (byte-wise on the UTF-8
   encoded key names).
3. **No insignificant whitespace** between tokens — compact form.
4. **Numbers**: integer-valued, no decimal point, no exponent, no
   leading zeros, no `+` sign. Range: signed 64-bit.
5. **Strings**: escape only `"`, `\`, and bytes `< 0x20`. Do NOT escape
   `/` or non-ASCII. Use `\uXXXX` only for control chars; UTF-8 bytes
   pass through verbatim.
6. **No trailing newline** inside the signed body.

### Example body (whitespace shown for clarity only — not signed form)

```json
{
  "code":   "aB3xZ9k",
  "exp":    1760000000,
  "nonce":  "a4f2b0c1e8d79b03",
  "scope":  "read-only",
  "signer": "keystone-root-2026",
  "uses":   5
}
```

Signed form (one line, as emitted):

```
{"code":"aB3xZ9k","exp":1760000000,"nonce":"a4f2b0c1e8d79b03","scope":"read-only","signer":"keystone-root-2026","uses":5}
```

## Signing

```
signing_input = ("v1." + body_b64).encode("ascii")   # ascii-safe by construction
signature     = ed25519_sign(private_key, signing_input)
sig_b64       = base64url_no_pad(signature)
token         = "v1." + body_b64 + "." + sig_b64
```

## Verification (every step mandatory)

A conformant verifier MUST:

1. Split the token on `.`. Reject if the result is not exactly 3 parts.
2. Reject if `parts[0] != "v1"`.
3. Decode `parts[1]` (`body_b64`) and `parts[2]` (`sig_b64`) with
   base64url-no-pad. Reject on any decode error.
4. Reconstruct `signing_input = ("v1." + parts[1]).encode("ascii")`.
5. Resolve the public key from the `signer` field in the body **after**
   parsing JSON. (Do NOT trust `signer` until signature verifies.)
   This is a two-pass check: tentatively parse, pick key, verify, then
   use.
6. `ed25519_verify(public_key, signing_input, sig)`. Reject on failure.
7. Reject if `code` or `scope` is empty.
8. If `exp != 0` and current Unix seconds > `exp`, reject as expired.
9. Return the parsed capability on success.

Verifiers MUST NOT:

- Trust ANY field in the body before signature verification succeeds —
  not even for picking the public key when `signer` is ambiguous; use
  constant-time key lookup if multiple keys might match.
- Downgrade to a weaker verification path on malformed input.
- Treat an expired-but-signature-valid token as valid.

## Replay and reuse

- `nonce` is 16 random bytes per issuance. Two capabilities for the same
  `(code, scope, exp, uses, signer)` MUST have different nonces, so
  revocation registries can target single issuances.
- `uses` is enforced by the verifier against a counter it maintains
  (Redis, DB) keyed by the capability's `(nonce, code)` or equivalent.
  **The token itself does not track usage** — a bearer could present the
  same token many times without state.
- Revocation lists (if used) key by `nonce`.

## Known test vectors

The reference implementation ships a test (`capability_test.go`) that
pins:

- Canonical JSON key order (`code,exp,nonce,scope,signer,uses`).
- Round-trip sign/verify with a known Ed25519 keypair.
- Signature failure on body tampering.
- Expiry enforcement when `now > exp`.

A conformant port replays those tests against the same test vectors
and MUST produce byte-identical signed bodies.

## Non-goals for v1

- Multiple scopes per token (v1 is single-scope).
- Public-key rotation protocols (callers manage key_id out of band).
- Capability delegation / sub-tokens.
- Confidential capabilities (payload is clear-text JSON by design;
  anyone can read it, only the signer can produce it).
