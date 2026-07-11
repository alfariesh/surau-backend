#!/usr/bin/env python3
"""Arabic normalization helpers for search keys, not evidence text."""

from __future__ import annotations

import re

from .search_key import PROFILE_NAME, PROFILE_VERSION, normalized_key  # noqa: F401 - compatibility re-export

ARABIC_MARKS_RE = re.compile(r"[\u0610-\u061a\u064b-\u065f\u0670\u06d6-\u06ed]")
TATWEEL = "\u0640"
SPACE_RE = re.compile(r"\s+")

ALEF_TRANSLATION = str.maketrans(
    {
        "أ": "ا",
        "إ": "ا",
        "آ": "ا",
        "ٱ": "ا",
        "ى": "ي",
    }
)

GENERIC_EXACTIONS = {
    "هو",
    "هي",
    "هم",
    "هما",
    "هذا",
    "هذه",
    "ذلك",
    "تلك",
    "قال",
    "قلت",
    "قيل",
    "يقول",
    "الشيخ",
    "الإمام",
    "الامام",
    "العلامة",
    "العالم",
    "المصنف",
    "الناظم",
    "الشارح",
}

COMMON_AMBIGUOUS_PERSON_NAMES = {
    "احمد",
    "محمد",
    "علي",
    "حسن",
    "الحسن",
    "حسين",
    "الحسين",
    "عمر",
    "عثمان",
    "عبدالله",
    "عبد الله",
    "ابو بكر",
    "ابي بكر",
    "ابى بكر",
}

THEONYM_KEYS = {
    "الله",
    "اللهم",
    "الاله",
    "الرب",
    "ربنا",
    "ربه",
    "سبحانه",
    "تعالي",
}

DEVOTIONAL_FORMULA_KEYS = {
    "بسم الله الرحمن الرحيم",
    "الحمد لله",
    "الحمد لله رب العالمين",
    "سبحان الله",
    "سبحانه وتعالي",
    "صلي الله عليه وسلم",
    "صلي الله عليه وسل م",
    "عليه الصلاة والسلام",
    "لا اله الا الله",
    "استغفر الله",
}

PERSON_REFERENCE_KEYS = {
    "النبي",
    "النبي صلي الله عليه وسلم",
    "النبي صلي الله عليه وسل م",
    "رسول الله",
    "رسول الله صلي الله عليه وسلم",
    "رسول الله صلي الله عليه وسل م",
    "الرسول",
    "المصطفي",
}

SURAH_PREFIXES = {
    "سوره",
    "سورة",
}


def strip_arabic_marks(value: str) -> str:
    """Remove tashkil and tatweel while preserving base letters."""
    return ARABIC_MARKS_RE.sub("", value).replace(TATWEEL, "")


def normalize_arabic(value: str) -> str:
    """Return a conservative Arabic search key for matching aliases."""
    value = strip_arabic_marks(value)
    value = value.translate(ALEF_TRANSLATION)
    value = value.replace("ؤ", "و").replace("ئ", "ي")
    value = SPACE_RE.sub(" ", value)
    return value.strip()


def normalize_grounding_char(value: str) -> str:
    """Normalize one character for evidence matching while preserving mappability."""
    if not value or ARABIC_MARKS_RE.fullmatch(value) or value == TATWEEL:
        return ""
    value = value.translate(ALEF_TRANSLATION)
    value = value.replace("ؤ", "و").replace("ئ", "ي")
    if value.isspace():
        return " "
    return value


def normalized_grounding_key(value: str) -> str:
    """Normalize Arabic for source-span fallback matching."""
    normalized = "".join(normalize_grounding_char(char) for char in value)
    return SPACE_RE.sub(" ", normalized).strip()


def is_generic_extraction(value: str) -> bool:
    """Return true for pronouns, lone reporting verbs, and generic titles."""
    return normalized_key(value) in GENERIC_EXACTIONS


def is_ambiguous_person_name(value: str) -> bool:
    """Flag short/common person mentions that should never be auto-merged."""
    key = normalized_key(value)
    if key in COMMON_AMBIGUOUS_PERSON_NAMES:
        return True
    parts = key.split()
    if len(parts) == 1 and parts[0] in COMMON_AMBIGUOUS_PERSON_NAMES:
        return True
    return False


def is_theonym(value: str) -> bool:
    """Return true for divine names that must not be typed as person."""
    return normalized_key(value) in THEONYM_KEYS


def is_devotional_formula(value: str) -> bool:
    """Return true for formulaic devotional phrases that are citation noise."""
    return normalized_key(value) in DEVOTIONAL_FORMULA_KEYS


def is_person_reference(value: str) -> bool:
    """Return true for title-like person references that need review."""
    return normalized_key(value) in PERSON_REFERENCE_KEYS


def is_surah_reference(value: str) -> bool:
    """Return true when a mention is a Quran surah reference, not a work title."""
    key = normalized_key(value)
    return any(key == prefix or key.startswith(f"{prefix} ") for prefix in SURAH_PREFIXES)
