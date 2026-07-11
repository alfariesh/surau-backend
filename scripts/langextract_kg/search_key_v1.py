"""Immutable Python implementation of the canonical search-key/v1 profile."""

from __future__ import annotations

from bisect import bisect_right
import json
from pathlib import Path
from typing import Final

PROFILE_NAME: Final = "search-key"
PROFILE_VERSION: Final = 1
SOURCE_UNICODE_VERSION: Final = "15.0.0"

_RANGES_PATH = (
    Path(__file__).resolve().parents[2]
    / "internal"
    / "quranutil"
    / "normalization_v1_unicode_ranges.json"
)


def _load_ranges() -> tuple[tuple[int, int], ...]:
    artifact = json.loads(_RANGES_PATH.read_bytes())
    if artifact.get("profile") != {"name": PROFILE_NAME, "version": PROFILE_VERSION}:
        raise RuntimeError("search-key/v1 Unicode table has the wrong profile")
    if artifact.get("source_unicode_version") != SOURCE_UNICODE_VERSION:
        raise RuntimeError("search-key/v1 Unicode table has the wrong source version")

    ranges = tuple((int(item[0]), int(item[1])) for item in artifact.get("ranges", ()))
    previous_hi = -1
    for lo, hi in ranges:
        if lo < 0 or hi > 0x10FFFF or lo > hi or lo <= previous_hi:
            raise RuntimeError("search-key/v1 Unicode table is malformed")
        previous_hi = hi
    if not ranges:
        raise RuntimeError("search-key/v1 Unicode table is empty")
    return ranges


_LETTER_DIGIT_RANGES: Final = _load_ranges()
_LETTER_DIGIT_STARTS: Final = tuple(item[0] for item in _LETTER_DIGIT_RANGES)

_CHAR_TRANSLATION: Final = {
    "أ": "ا",
    "إ": "ا",
    "آ": "ا",
    "ٱ": "ا",
    "ى": "ي",
    "ؤ": "و",
    "ئ": "ي",
}


def is_v1_letter_or_digit(char: str) -> bool:
    """Classify one code point using the frozen Go Unicode 15.0 table."""
    if len(char) != 1:
        raise ValueError("expected exactly one code point")
    code_point = ord(char)
    index = bisect_right(_LETTER_DIGIT_STARTS, code_point) - 1
    return index >= 0 and code_point <= _LETTER_DIGIT_RANGES[index][1]


def _is_removed_arabic_mark(code_point: int) -> bool:
    return (
        0x0610 <= code_point <= 0x061A
        or 0x064B <= code_point <= 0x065F
        or code_point == 0x0670
        or 0x06D6 <= code_point <= 0x06ED
    )


def normalize_search_key_v1(value: str) -> str:
    """Return exactly the key produced by Go NormalizeKeyV1."""
    canonical: list[str] = []
    for char in value:
        code_point = ord(char)
        if _is_removed_arabic_mark(code_point) or char == "ـ":
            continue
        normalized = _CHAR_TRANSLATION.get(char, char)
        canonical.append(normalized if is_v1_letter_or_digit(normalized) else " ")

    # Every separator above is ASCII space, keeping whitespace semantics frozen.
    return " ".join("".join(canonical).split())


__all__ = [
    "PROFILE_NAME",
    "PROFILE_VERSION",
    "SOURCE_UNICODE_VERSION",
    "is_v1_letter_or_digit",
    "normalize_search_key_v1",
]
