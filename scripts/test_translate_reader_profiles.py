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


TRANSLATION_GENERATION = {
    "run_id": "44444444-4444-4444-8444-444444444444",
    "model_id": "deepseek-v4-flash",
    "prompt_version": "reader-translation-v1",
}
SUMMARY_TRANSLATION_GENERATION = {
    "run_id": "55555555-5555-4555-8555-555555555555",
    "model_id": "glm-5.1",
    "prompt_version": "reader-summary-translation-v1",
}


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
        calls: list[tuple[str, str, dict[str, object]]] = []

        class FakeClient:
            def invoke(self, task: str, user: str, **kwargs: object) -> dict[str, object]:
                calls.append((task, user, kwargs))
                return {"output": json.dumps({"title": "Title", "content": "Body content"})}

        translated, _ = tr.translate_section(
            client=FakeClient(),  # type: ignore[arg-type]
            target_lang="id",
            book_metadata={"name": "كتاب الفقه", "category_id": 2, "category_name": "فقه"},
            profile_name="fiqh",
            profile_source="manual",
            profile_config=self.profile_map["profiles"]["fiqh"],
            source_title="باب الطهارة",
            source_text="نص عربي",
        )

        self.assertEqual(translated["title"], "Title")
        self.assertEqual(calls[0][0], "reader-translation")
        user_content = calls[0][1]
        self.assertIn('"translation_profile": "fiqh"', user_content)
        self.assertIn('"profile_style_guide"', user_content)
        self.assertIn('"category_name": "فقه"', user_content)

    def test_heading_asset_metadata_contains_profile(self) -> None:
        original_fetch = tr.fetch_toc_section
        original_translate = tr.translate_section

        def fake_fetch(base_url: str, book_id: int, heading_id: int, lang: str) -> dict[str, object]:
            return {"title": "باب", "original_text": "نص عربي طويل"}

        def fake_translate(**kwargs: object) -> tuple[dict[str, str], dict[str, object]]:
            del kwargs
            return (
                {"title": "Bab", "content": "Konten terjemahan yang cukup panjang."},
                {
                    "generation": TRANSLATION_GENERATION,
                    "provider": "deepseek",
                    "model": TRANSLATION_GENERATION["model_id"],
                },
            )

        tr.fetch_toc_section = fake_fetch
        tr.translate_section = fake_translate
        try:
            args = argparse.Namespace(
                base_url="http://127.0.0.1:8080",
                book_id=10,
                source_lang="ar",
                target_lang="id",
                max_source_chars=0,
                dry_run=False,
                book_metadata={"category_id": 2, "category_name": "فقه"},
                selected_profile="fiqh",
                selected_profile_source="manual",
                selected_profile_config=self.profile_map["profiles"]["fiqh"],
            )
            asset = tr.translate_heading_asset(args, object(), 5, 1, 1)
        finally:
            tr.fetch_toc_section = original_fetch
            tr.translate_section = original_translate

        metadata = asset["metadata"]
        self.assertEqual(metadata["style_version"], "reader-profile-v1")
        self.assertEqual(metadata["translation_profile"], "fiqh")
        self.assertEqual(metadata["profile_source"], "manual")
        self.assertEqual(metadata["category_id"], 2)
        self.assertEqual(asset["provenance_class"], "machine")
        self.assertEqual(asset["generation"], TRANSLATION_GENERATION)

    def test_summary_only_asset_translates_source_summary(self) -> None:
        original_fetch = tr.fetch_toc_section
        original_translate_summary = tr.translate_summary

        def fake_fetch(base_url: str, book_id: int, heading_id: int, lang: str) -> dict[str, object]:
            return {"title": "باب", "summary": "يتناول الباب معنى التقوى.", "summary_lang": "ar"}

        def fake_translate_summary(**kwargs: object) -> tuple[str, dict[str, object]]:
            self.assertEqual(kwargs["source_summary"], "يتناول الباب معنى التقوى.")
            return (
                "Bab ini menjelaskan makna takwa.",
                {
                    "generation": SUMMARY_TRANSLATION_GENERATION,
                    "provider": "sumopod",
                    "model": SUMMARY_TRANSLATION_GENERATION["model_id"],
                },
            )

        tr.fetch_toc_section = fake_fetch
        tr.translate_summary = fake_translate_summary
        try:
            args = argparse.Namespace(
                base_url="http://127.0.0.1:8080",
                book_id=10,
                source_lang="ar",
                target_lang="id",
                max_source_chars=0,
                dry_run=False,
                include_summary=False,
                summary_only=True,
                book_metadata={"category_id": 2, "category_name": "تزكية"},
                selected_profile="general",
                selected_profile_source="manual",
                selected_profile_config=self.profile_map["profiles"]["general"],
            )
            assets = tr.translate_heading_assets(args, object(), 5, 1, 1)
        finally:
            tr.fetch_toc_section = original_fetch
            tr.translate_summary = original_translate_summary

        self.assertEqual(len(assets), 1)
        self.assertEqual(assets[0]["kind"], "heading_summary")
        self.assertEqual(assets[0]["summary"], "Bab ini menjelaskan makna takwa.")
        self.assertEqual(assets[0]["metadata"]["source_lang"], "ar")
        self.assertEqual(assets[0]["provenance_class"], "machine")
        self.assertEqual(assets[0]["generation"], SUMMARY_TRANSLATION_GENERATION)

    def test_invocation_uses_distinct_runs_for_each_prompt_family(self) -> None:
        args = argparse.Namespace(
            model="test-model",
            include_summary=True,
            summary_only=False,
        )

        tr.initialize_generation_runs(args)

        self.assertNotEqual(
            args.translation_generation["run_id"],
            args.summary_translation_generation["run_id"],
        )
        self.assertEqual(
            args.translation_generation["prompt_version"],
            "reader-translation-v1",
        )
        self.assertEqual(
            args.summary_translation_generation["prompt_version"],
            "reader-summary-translation-v1",
        )


if __name__ == "__main__":
    unittest.main()
