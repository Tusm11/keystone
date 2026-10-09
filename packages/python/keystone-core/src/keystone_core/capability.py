"""Capability tokens — Python port of docs/capability-spec.md v1.

The Go reference implementation (origin/internal/primitives/capability)
is one conformant implementation; this is another. Both must produce
byte-identical signed bodies for the same inputs — the test vectors
enforce it.

Wire format recap (full spec in docs/capability-spec.md):

    token    = "v1." + body_b64 + "." + sig_b64
    body     = canonical JSON of {code, exp, nonce, scope, signer, uses}
    body_b64 = base64url-no-pad(body)
    sig_b64  = base64url-no-pad(Ed25519-sign("v1." + body_b64))

Canonical JSON rules enforced here:
  - keys sorted ascending (byte-wise on UTF-8 encoded names)
  - compact separators (',', ':'); no whitespace
  - UTF-8 bytes preserved (ensure_ascii=False); escape only control chars + '"' + '\\'

Python's json.dumps with (sort_keys=True, separators=(',', ':'),
ensure_ascii=False) matches the spec and the Go implementation's output
byte-for-byte.
"""

from __future__ import annotations

import base64
import json
import re
import secrets
import time
from dataclasses import dataclass, field, asdict
from typing import Optional

from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import (
    Ed25519PrivateKey,
    Ed25519PublicKey,
)

Version = "v1"


# --- errors ------------------------------------------------------------------

class CapabilityError(Exception):
    """Base error for capability operations."""


class MalformedError(CapabilityError):
    """Token structure is malformed (shape, encoding, JSON)."""


class BadVersionError(CapabilityError):
    """Token has an unsupported version prefix."""


class BadSignatureError(CapabilityError):
    """Signature doesn't verify under the given public key."""


class ExpiredError(CapabilityError):
    """Token's exp has passed."""


class EmptyScopeError(CapabilityError):
    """Scope field is empty."""


class EmptyCodeError(CapabilityError):
    """Code field is empty."""


# --- data model --------------------------------------------------------------

@dataclass
class Capability:
    """Decoded capability body.

    Field order in this class is NOT the wire order — canonical JSON
    sorts keys lexicographically. Attribute order here is arbitrary.
    """

    code: str
    scope: str
    signer: str
    exp: int = 0        # Unix seconds; 0 = never expires
    uses: int = 0       # 0 = unlimited
    nonce: str = ""     # 16 random bytes, lowercase hex (32 chars)

    def __post_init__(self) -> None:
        if not self.code:
            raise EmptyCodeError("code is required")
        if not self.scope:
            raise EmptyScopeError("scope is required")
        if not self.nonce:
            self.nonce = secrets.token_hex(16)

    def expires_at(self) -> Optional[int]:
        """Return the expiry as a Unix timestamp, or None if no expiry."""
        return self.exp or None

    def sign(self, private_key: Ed25519PrivateKey) -> str:
        """Serialize + sign + encode as a v1 token."""
        return sign_capability(self, private_key)


# --- canonical JSON ----------------------------------------------------------

def _canonical_json(cap: Capability) -> bytes:
    """Produce the signed-body bytes.

    Must match the Go reference. The trick: Python's json.dumps with
    sort_keys produces lex-sorted keys with no whitespace. ensure_ascii
    is False so non-ASCII bytes pass through verbatim (same as Go).
    """
    body = {
        "code": cap.code,
        "scope": cap.scope,
        "signer": cap.signer,
        "exp": int(cap.exp),
        "uses": int(cap.uses),
        "nonce": cap.nonce,
    }
    return json.dumps(
        body,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
    ).encode("utf-8")


# --- base64url-no-pad --------------------------------------------------------

def _b64url_encode(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")


# Strict base64url alphabet — matches Go's RawURLEncoding.DecodeString,
# which errors on any char outside [A-Za-z0-9_-]. Python's
# urlsafe_b64decode is tolerant by default and silently drops bad chars,
# so we validate ourselves before decoding.
_B64URL_RE = re.compile(r"^[A-Za-z0-9_\-]*$")


def _b64url_decode(s: str) -> bytes:
    if not _B64URL_RE.match(s):
        raise ValueError("non-base64url character")
    pad = (-len(s)) % 4
    return base64.urlsafe_b64decode(s + "=" * pad)


# --- sign / verify -----------------------------------------------------------

def sign_capability(cap: Capability, private_key: Ed25519PrivateKey) -> str:
    """Sign the capability and return the three-part token.

    The signing input is `"v1." + body_b64` as ASCII bytes — the version
    prefix is included in the signed data, not just the body. This is
    what prevents a cross-version downgrade attack.
    """
    if not cap.code:
        raise EmptyCodeError("code is required")
    if not cap.scope:
        raise EmptyScopeError("scope is required")

    body = _canonical_json(cap)
    body_b64 = _b64url_encode(body)
    signing_input = f"{Version}.{body_b64}".encode("ascii")

    signature = private_key.sign(signing_input)
    sig_b64 = _b64url_encode(signature)
    return f"{Version}.{body_b64}.{sig_b64}"


def verify_capability(
    token: str,
    public_key: Ed25519PublicKey,
    now: Optional[int] = None,
) -> Capability:
    """Verify + decode a token. Returns the Capability on success.

    - Rejects malformed tokens, unknown versions, and failed signatures.
    - Rejects empty-code or empty-scope bodies (even if signature passes).
    - If `now` is set and the token has an expiry, rejects expired tokens.
      Pass `now=0` to skip expiry (useful for test fixtures).

    This is the ONLY code path callers should treat a token as trusted.
    """
    parts = token.split(".")
    if len(parts) != 3:
        raise MalformedError("expected 3 dot-separated parts")
    if parts[0] != Version:
        raise BadVersionError(f"unsupported version: {parts[0]!r}")

    try:
        body = _b64url_decode(parts[1])
        sig = _b64url_decode(parts[2])
    except ValueError as e:
        raise MalformedError(f"base64 decode: {e}") from e

    if len(sig) != 64:
        # Ed25519 signatures are always 64 bytes; a wrong length is not
        # a signature failure, it's a malformed token.
        raise MalformedError(f"signature must be 64 bytes, got {len(sig)}")

    signing_input = f"{Version}.{parts[1]}".encode("ascii")
    try:
        public_key.verify(sig, signing_input)
    except InvalidSignature as e:
        raise BadSignatureError("signature verification failed") from e

    try:
        obj = json.loads(body)
    except json.JSONDecodeError as e:
        raise MalformedError(f"body is not valid JSON: {e}") from e
    if not isinstance(obj, dict):
        raise MalformedError("body is not a JSON object")

    try:
        cap = Capability(
            code=obj["code"],
            scope=obj["scope"],
            signer=obj.get("signer", ""),
            exp=int(obj.get("exp", 0)),
            uses=int(obj.get("uses", 0)),
            nonce=obj.get("nonce", ""),
        )
    except (KeyError, EmptyCodeError, EmptyScopeError) as e:
        if isinstance(e, (EmptyCodeError, EmptyScopeError)):
            raise
        raise MalformedError(f"body missing required field: {e}") from e

    if now is None:
        now = int(time.time())
    if now != 0 and cap.exp != 0 and now > cap.exp:
        raise ExpiredError(f"token expired at {cap.exp}")

    return cap
