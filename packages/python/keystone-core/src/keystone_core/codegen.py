"""Short URL code generator — pure, cryptographically random base62.

Mirror of origin/internal/primitives/codegen/codegen.go. Same alphabet,
same algorithm. Tested against the Go implementation to ensure the
produced codes satisfy identical validity rules.
"""

import secrets

ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"


def random_code(length: int = 7) -> str:
    """Generate a cryptographically random base62 code.

    Length 7 gives 62^7 ≈ 3.5 trillion combinations. Non-positive length
    collapses to 7 (same default as the Go side).

    Uses `secrets` (Python's crypto-grade RNG). Never `random` — predictable
    codes become a security surface in a URL shortener (link enumeration).
    """
    if length <= 0:
        length = 7
    return "".join(secrets.choice(ALPHABET) for _ in range(length))
