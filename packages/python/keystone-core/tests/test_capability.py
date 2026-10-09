import time

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from keystone_core import (
    Capability,
    sign_capability,
    verify_capability,
    BadSignatureError,
    BadVersionError,
    EmptyCodeError,
    EmptyScopeError,
    ExpiredError,
    MalformedError,
)


def new_keys():
    priv = Ed25519PrivateKey.generate()
    return priv, priv.public_key()


def test_round_trip():
    priv, pub = new_keys()
    cap = Capability(code="aB3xZ9k", scope="read-only", signer="pytest",
                     exp=int(time.time()) + 3600, uses=5)
    token = cap.sign(priv)
    assert token.startswith("v1.")
    assert token.count(".") == 2

    back = verify_capability(token, pub)
    assert back.code == cap.code
    assert back.scope == cap.scope
    assert back.uses == 5
    assert back.signer == "pytest"


def test_tampered_body_fails_signature():
    priv, pub = new_keys()
    cap = Capability(code="abc", scope="read-only", signer="s")
    token = cap.sign(priv)
    parts = token.split(".")
    tampered = f"{parts[0]}.{parts[1].upper()}.{parts[2]}"
    with pytest.raises((BadSignatureError, MalformedError)):
        verify_capability(tampered, pub)


def test_wrong_public_key_rejected():
    priv, _ = new_keys()
    _, other_pub = new_keys()
    cap = Capability(code="abc", scope="read-only", signer="s")
    token = cap.sign(priv)
    with pytest.raises(BadSignatureError):
        verify_capability(token, other_pub)


def test_expired_token_rejected():
    priv, pub = new_keys()
    cap = Capability(code="abc", scope="read-only", signer="s",
                     exp=int(time.time()) - 60)
    token = cap.sign(priv)
    with pytest.raises(ExpiredError):
        verify_capability(token, pub)
    # Passing now=0 skips expiry — useful for fixtures.
    assert verify_capability(token, pub, now=0) is not None


def test_malformed_tokens():
    _, pub = new_keys()
    bad = ["", "v1.body", "v1.body.sig.extra", "v1.!!!.sig"]
    for b in bad:
        with pytest.raises((MalformedError, BadVersionError)):
            verify_capability(b, pub)


def test_bad_version():
    priv, pub = new_keys()
    cap = Capability(code="abc", scope="read-only", signer="s")
    token = cap.sign(priv)
    parts = token.split(".")
    with pytest.raises(BadVersionError):
        verify_capability(f"v2.{parts[1]}.{parts[2]}", pub)


def test_empty_code_rejected_at_construction():
    with pytest.raises(EmptyCodeError):
        Capability(code="", scope="read-only", signer="s")


def test_empty_scope_rejected_at_construction():
    with pytest.raises(EmptyScopeError):
        Capability(code="abc", scope="", signer="s")


def test_fresh_capabilities_get_distinct_nonces():
    a = Capability(code="x", scope="r", signer="s")
    b = Capability(code="x", scope="r", signer="s")
    assert a.nonce != b.nonce
