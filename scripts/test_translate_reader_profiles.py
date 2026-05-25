#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import sys
import unittest
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

import translate_reader_assets as tr  # noqa: E402


class TranslationProfileTest(unittest.TestCase):
    def setUp(self) -> None:
        self.profile_map = tr.load_translation_profiles(SCRIPT_DIR / "translation_profiles.json")

    def test_auto_profile_falls_back_to_general(self) -> None:
        profile, source = tr.resolve_translation_profile(
            "auto",
            self.profile_map,
            {"name": "كتاب غير مصنف", "category_name": "متفرقات"},
        )

        self.assertEqual(profile, "general")
        self.assertEqual(source, "auto:default")

    def test_manual_profile_overrides_detection(self) -> None:
        profile, source = tr.resolve_translation_profile(
            "fiqh",
            self.profile_map,
            {"name": "الأربعين النووية", "category_name": "حديث"},
        )

        self.assertEqual(profile, "fiqh")
        self.assertEqual(source, "manual")

    def test_auto_profile_uses_category_metadata(self) -> None:
        profile, source = tr.resolve_translation_profile(
            "auto",
            self.profile_map,
            {"name": "مختصر", "category_name": "النحو والصرف"},
        )

        self.assertEqual(profile, "arabic_language")
        self.assertIn("category:", source)

    def test_translate_section_payload_contains_profile_context(self) -> None:
        calls: list[dict[str, object]] = []
        original_request_json = tr.request_json

        def fake_request_json(method: str, url: str, **kwargs: object) -> dict[str, object]:
            calls.append({"method": method, "url": url, "kwargs": kwargs})
            return {"choices": [{"message": {"content": json.dumps({"title": "Title", "content": "Body content"})}}]}

        tr.request_json = fake_request_json
        try:
            translated = tr.translate_section(
                api_key="test-key",
                deepseek_base_url="https://example.test",
                model="deepseek-v4-flash",
                target_lang="id",
                book_metadata={"name": "كتاب الفقه", "category_id": 2, "category_name": "فقه"},
                profile_name="fiqh",
                profile_source="manual",
                profile_config=self.profile_map["profiles"]["fiqh"],
                source_title="باب الطهارة",
                source_text="نص عربي",
                max_tokens=500,
                timeout_seconds=1,
                retries=0,
            )
        finally:
            tr.request_json = original_request_json

        self.assertEqual(translated["title"], "Title")
        payload = calls[0]["kwargs"]["payload"]  # type: ignore[index]
        user_content = payload["messages"][1]["content"]  # type: ignore[index]
        self.assertIn('"translation_profile": "fiqh"', user_content)
        self.assertIn('"profile_style_guide"', user_content)
        self.assertIn('"category_name": "فقه"', user_content)

    def test_heading_asset_metadata_contains_profile(self) -> None:
        original_fetch = tr.fetch_toc_section
        original_translate = tr.translate_section

        def fake_fetch(base_url: str, book_id: int, heading_id: int, lang: str) -> dict[str, object]:
            return {"title": "باب", "original_text": "نص عربي طويل"}

        def fake_translate(**kwargs: object) -> dict[str, str]:
            return {"title": "Bab", "content": "Konten terjemahan yang cukup panjang."}

        tr.fetch_toc_section = fake_fetch
        tr.translate_section = fake_translate
        try:
            args = argparse.Namespace(
                base_url="http://127.0.0.1:8080",
                book_id=10,
                source_lang="ar",
                target_lang="id",
                model="deepseek-v4-flash",
                deepseek_base_url="https://example.test",
                max_source_chars=0,
                max_tokens=500,
                timeout_seconds=1,
                retries=0,
                dry_run=False,
                book_metadata={"category_id": 2, "category_name": "فقه"},
                selected_profile="fiqh",
                selected_profile_source="manual",
                selected_profile_config=self.profile_map["profiles"]["fiqh"],
            )
            asset = tr.translate_heading_asset(args, "test-key", 5, 1, 1)
        finally:
            tr.fetch_toc_section = original_fetch
            tr.translate_section = original_translate

        metadata = asset["metadata"]
        self.assertEqual(metadata["style_version"], "reader-profile-v1")
        self.assertEqual(metadata["translation_profile"], "fiqh")
        self.assertEqual(metadata["profile_source"], "manual")
        self.assertEqual(metadata["category_id"], 2)


if __name__ == "__main__":
    unittest.main()
