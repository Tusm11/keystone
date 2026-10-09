"""keystone-core — signed capability tokens + short-code primitives.

Python port of the Go `internal/primitives/*` packages. The two
implementations verify each other's output byte-for-byte, enforced by
the shared test vectors in docs/test-vectors/.

Public API:

    from keystone_core import Capability, verify_capability, random_code

    cap = Capability(code="aB3xZ9k", scope="read-only", signer="my-service",
                     exp=0, uses=0, nonce=None)   # nonce auto-generated
    token = cap.sign(private_key)
    cap_back = verify_capability(token, public_key)

Spec: see docs/capability-spec.md for the authoritative wire format.
"""

from .capability import (
    Capability,
    Version,
    verify_capability,
    sign_capability,
    MalformedError,
    BadVersionError,
    BadSignatureError,
    ExpiredError,
    EmptyScopeError,
    EmptyCodeError,
)
from .codegen import random_code

__all__ = [
    "Capability",
    "Version",
    "verify_capability",
    "sign_capability",
    "random_code",
    "MalformedError",
    "BadVersionError",
    "BadSignatureError",
    "ExpiredError",
    "EmptyScopeError",
    "EmptyCodeError",
]

__version__ = "0.1.0"
