from __future__ import annotations

import unittest
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from langextract_kg.arabic_normalize import (  # noqa: E402
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


class ArabicNormalizeTest(unittest.TestCase):
    def test_strip_marks_and_tatweel(self) -> None:
        self.assertEqual(strip_arabic_marks("أَبُو حَامِدٍ ـ الغزالي"), "أبو حامد  الغزالي")

    def test_normalized_key(self) -> None:
        self.assertEqual(normalized_key("إحياءُ علومِ الدّين"), "احياء علوم الدين")

    def test_normalized_grounding_key_preserves_evidence_shape_less_aggressively(self) -> None:
        self.assertEqual(normalized_grounding_key("رسولِ اللَّهِ   صلى الله عليه وسلم"), "رسول الله صلي الله عليه وسلم")

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


if __name__ == "__main__":
    unittest.main()
