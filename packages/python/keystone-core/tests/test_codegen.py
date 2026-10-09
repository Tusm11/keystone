import re
import pytest

from keystone_core import random_code

ALPHABET_RE = re.compile(r"^[A-Za-z0-9]+$")


def test_default_length_is_seven():
    for _ in range(20):
        assert len(random_code()) == 7


@pytest.mark.parametrize("n", [1, 6, 7, 12, 32])
def test_requested_length(n):
    assert len(random_code(n)) == n


@pytest.mark.parametrize("n", [0, -1, -100])
def test_non_positive_falls_back_to_seven(n):
    assert len(random_code(n)) == 7


def test_only_base62_alphabet():
    for _ in range(100):
        assert ALPHABET_RE.match(random_code(16))


def test_uniqueness_across_1000_samples():
    # 62^7 ≈ 3.5e12; expect zero collisions in 1000 samples.
    seen = set()
    for _ in range(1000):
        c = random_code(7)
        assert c not in seen, f"duplicate: {c}"
        seen.add(c)
