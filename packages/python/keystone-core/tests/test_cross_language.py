"""Cross-language test vectors.

These vectors are produced by the Go implementation
(origin/cmd/gentestvectors) and committed under docs/test-vectors/.
Both the Go test suite and this Python test suite consume the same file.

If the Python port drifts from the spec by even one byte of canonical
JSON, these tests fail. That's the whole mechanism — the spec is
enforced across languages, not just documented.
"""

import base64
import json
import os
from pathlib import Path

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import (
    Ed25519PrivateKey,
    Ed25519PublicKey,
)

from keystone_core import Capability, sign_capability, verify_capability


def _find_vectors() -> Path:
    """Walk up until we find docs/test-vectors/capability.json."""
    here = Path(__file__).resolve().parent
    for parent in [here, *here.parents]:
        candidate = parent / "docs" / "test-vectors" / "capability.json"
        if candidate.exists():
            return candidate
    pytest.skip(
        "docs/test-vectors/capability.json not found. "
        "Generate it with: go run ./origin/cmd/gentestvectors > docs/test-vectors/capability.json"
    )


def _b64url_decode(s: str) -> bytes:
    pad = (-len(s)) % 4
    return base64.urlsafe_b64decode(s + "=" * pad)


@pytest.fixture(scope="module")
def vectors():
    path = _find_vectors()
    bundle = json.loads(path.read_text())
    assert bundle["spec_version"] == "v1", f"unknown spec version: {bundle['spec_version']}"
    return bundle["vectors"]


def test_vectors_file_has_at_least_one(vectors):
    assert len(vectors) >= 1


def test_python_resign_matches_go_byte_for_byte(vectors):
    """The core cross-language guarantee: given the same inputs and the
    same private key, Python produces the exact same signed token Go did.
    Any drift in canonical JSON, base64 encoding, or signing input
    construction shows up here as a diff."""
    for v in vectors:
        priv_bytes = _b64url_decode(v["private_key_b64"])
        priv = Ed25519PrivateKey.from_private_bytes(priv_bytes[:32])  # Ed25519 private key is the first 32 bytes (seed)

        cap = Capability(
            code=v["capability"]["code"],
            scope=v["capability"]["scope"],
            signer=v["capability"]["signer"],
            exp=v["capability"]["exp"],
            uses=v["capability"]["uses"],
            nonce=v["capability"]["nonce"],
        )
        reproduced = sign_capability(cap, priv)
        assert reproduced == v["expected_token"], (
            f"vector {v['name']}: Python-signed token differs from Go-signed token.\n"
            f"  go:     {v['expected_token']}\n"
            f"  python: {reproduced}"
        )


def test_python_verifies_go_signed_tokens(vectors):
    """Python verifier accepts tokens signed by Go."""
    for v in vectors:
        pub_bytes = _b64url_decode(v["public_key_b64"])
        pub = Ed25519PublicKey.from_public_bytes(pub_bytes)
        cap = verify_capability(v["expected_token"], pub, now=0)
        assert cap.code == v["capability"]["code"]
        assert cap.scope == v["capability"]["scope"]
        assert cap.signer == v["capability"]["signer"]
        assert cap.exp == v["capability"]["exp"]
        assert cap.uses == v["capability"]["uses"]
        assert cap.nonce == v["capability"]["nonce"]
