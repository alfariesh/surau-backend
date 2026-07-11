from __future__ import annotations

import hashlib
import json
import unittest
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from langextract_kg.arabic_normalize import (  # noqa: E402
    PROFILE_NAME,
    PROFILE_VERSION,
    is_ambiguous_person_name,
    is_devotional_formula,
    is_generic_extraction,
    is_person_reference,
    is_surah_reference,
    is_theonym,
    normalized_grounding_key,
    normalized_key,
    strip_arabic_marks,
)
from langextract_kg.search_key_v1 import (  # noqa: E402
    SOURCE_UNICODE_VERSION,
    is_v1_letter_or_digit,
)


GOLDEN_CORPUS = Path(__file__).resolve().parents[2] / "internal" / "quranutil" / "normalization_v1_vectors.json"
GOLDEN_SHA256 = "c3b5e72310e8b35684769b26cf7a4fd6ade8debedd10505745bb941569097bb7"
UNICODE_CORPUS = Path(__file__).resolve().parents[2] / "internal" / "quranutil" / "normalization_v1_unicode_ranges.json"
UNICODE_SHA256 = "0f6a0f2050b296525bd83f5b107d6474341f7e8e6410b33ea008bb04ac62cd91"


class ArabicNormalizeTest(unittest.TestCase):
    def test_strip_marks_and_tatweel(self) -> None:
        self.assertEqual(strip_arabic_marks("أَبُو حَامِدٍ ـ الغزالي"), "أبو حامد  الغزالي")

    def test_normalized_key(self) -> None:
        self.assertEqual(normalized_key("إحياءُ علومِ الدّين"), "احياء علوم الدين")

    def test_search_key_v1_matches_shared_go_golden_corpus(self) -> None:
        raw = GOLDEN_CORPUS.read_bytes()
        self.assertEqual(hashlib.sha256(raw).hexdigest(), GOLDEN_SHA256)

        corpus = json.loads(raw)
        self.assertEqual(corpus["profile"], {"name": PROFILE_NAME, "version": PROFILE_VERSION})
        self.assertEqual(PROFILE_NAME, "search-key")
        self.assertEqual(PROFILE_VERSION, 1)
        self.assertTrue(corpus["vectors"])

        for vector in corpus["vectors"]:
            with self.subTest(vector=vector["name"]):
                actual = normalized_key(vector["input"])
                self.assertEqual(actual, vector["expected"])
                self.assertEqual(normalized_key(actual), actual)

    def test_search_key_v1_unicode_parity_is_exhaustive(self) -> None:
        raw = UNICODE_CORPUS.read_bytes()
        self.assertEqual(hashlib.sha256(raw).hexdigest(), UNICODE_SHA256)
        corpus = json.loads(raw)
        self.assertEqual(corpus["profile"], {"name": PROFILE_NAME, "version": PROFILE_VERSION})
        self.assertEqual(corpus["source_unicode_version"], SOURCE_UNICODE_VERSION)

        ranges = [(int(item[0]), int(item[1])) for item in corpus["ranges"]]
        range_index = 0
        input_chars: list[str] = []
        expected_tokens: list[str] = []

        for code_point in range(0x110000):
            while range_index < len(ranges) and code_point > ranges[range_index][1]:
                range_index += 1
            expected_class = (
                range_index < len(ranges) and code_point >= ranges[range_index][0]
            )
            char = chr(code_point)
            actual_class = is_v1_letter_or_digit(char)
            if actual_class != expected_class:
                self.fail(
                    f"classification mismatch at U+{code_point:04X}: "
                    f"got {actual_class}, want {expected_class}"
                )

            input_chars.extend((char, "|"))
            token = self._expected_single_char_v1(char, expected_class)
            if token:
                expected_tokens.append(token)

            if (code_point + 1) % 4096 == 0 or code_point == 0x10FFFF:
                actual = normalized_key("".join(input_chars))
                expected = " ".join(expected_tokens)
                if actual != expected:
                    self.fail(f"normalization mismatch in batch ending U+{code_point:04X}")
                input_chars.clear()
                expected_tokens.clear()

    def test_normalized_grounding_key_preserves_evidence_shape_less_aggressively(self) -> None:
        self.assertEqual(normalized_grounding_key("رسولِ اللَّهِ   صلى الله عليه وسلم"), "رسول الله صلي الله عليه وسلم")
        self.assertEqual(normalized_grounding_key("قال،زيد"), "قال،زيد")
        self.assertEqual(normalized_key("قال،زيد"), "قال زيد")

    def test_generic_extraction(self) -> None:
        self.assertTrue(is_generic_extraction("قال"))
        self.assertTrue(is_generic_extraction("الإمام"))
        self.assertFalse(is_generic_extraction("الإمام الشافعي"))

    def test_ambiguous_person_name(self) -> None:
        self.assertTrue(is_ambiguous_person_name("أحمد"))
        self.assertTrue(is_ambiguous_person_name("أبو بكر"))
        self.assertFalse(is_ambiguous_person_name("أبو حامد الغزالي"))

    def test_theonym(self) -> None:
        self.assertTrue(is_theonym("اللَّه"))
        self.assertTrue(is_theonym("الرّب"))
        self.assertFalse(is_theonym("عبد الله"))

    def test_devotional_formula(self) -> None:
        self.assertTrue(is_devotional_formula("بِسْمِ اللهِ الرَّحْمٰنِ الرَّحِيم"))
        self.assertTrue(is_devotional_formula("صلى الله عليه وسلم"))
        self.assertFalse(is_devotional_formula("إنما الأعمال بالنيات"))

    def test_person_reference(self) -> None:
        self.assertTrue(is_person_reference("رسول الله صلى الله عليه وسلّم"))
        self.assertTrue(is_person_reference("النبي"))
        self.assertFalse(is_person_reference("أبو حامد الغزالي"))

    def test_surah_reference(self) -> None:
        self.assertTrue(is_surah_reference("سورة البقرة"))
        self.assertFalse(is_surah_reference("صحيح البخاري"))

    @staticmethod
    def _expected_single_char_v1(char: str, accepted: bool) -> str:
        code_point = ord(char)
        if (
            0x0610 <= code_point <= 0x061A
            or 0x064B <= code_point <= 0x065F
            or code_point == 0x0670
            or 0x06D6 <= code_point <= 0x06ED
            or char == "ـ"
        ):
            return ""
        translated = {
            "أ": "ا",
            "إ": "ا",
            "آ": "ا",
            "ٱ": "ا",
            "ى": "ي",
            "ؤ": "و",
            "ئ": "ي",
        }.get(char)
        if translated is not None:
            return translated
        return char if accepted else ""


if __name__ == "__main__":
    unittest.main()
