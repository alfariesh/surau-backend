from __future__ import annotations

from pathlib import Path
import sys
import unittest

from langextract.core import exceptions

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from langextract_kg.openai_compatible_model import (  # noqa: E402
    OpenAICompatibleJSONModel,
    build_json_retry_prompt,
    classify_langextract_json,
    coerce_json_output,
    extract_message_text,
    normalize_langextract_json,
    sha256_text,
)


class _Message:
    def __init__(self, content: str = "", reasoning_content: str = "") -> None:
        self.content = content
        self.reasoning_content = reasoning_content


class _Choice:
    def __init__(self, message: object) -> None:
        self.message = message


class _Response:
    def __init__(self, message: object) -> None:
        self.choices = [_Choice(message)]


class _RetryModel(OpenAICompatibleJSONModel):
    def __init__(self, failures_before_success: int) -> None:
        self.max_api_retries = 2
        self.api_retry_sleep_seconds = 0
        self.failures_before_success = failures_before_success
        self.calls = 0

    def _complete(self, prompt: str, config: dict[str, object]) -> str:
        del prompt, config
        self.calls += 1
        if self.calls <= self.failures_before_success:
            raise exceptions.InferenceRuntimeError("temporary api failure")
        return '{"extractions": []}'


class OpenAICompatibleModelTest(unittest.TestCase):
    def test_extracts_content_first(self) -> None:
        self.assertEqual(extract_message_text(_Response(_Message(content=" {} "))), "{}")

    def test_falls_back_to_reasoning_content(self) -> None:
        self.assertEqual(
            extract_message_text(_Response(_Message(reasoning_content=' {"extractions": []} '))),
            '{"extractions": []}',
        )

    def test_supports_dict_message(self) -> None:
        self.assertEqual(
            extract_message_text(_Response({"content": "", "reasoning_content": '{"x": 1}'})),
            '{"x": 1}',
        )

    def test_coerces_fenced_json(self) -> None:
        self.assertEqual(coerce_json_output('```json\n{"extractions": []}\n```'), '{"extractions": []}')

    def test_coerces_prefixed_json(self) -> None:
        self.assertEqual(
            coerce_json_output('Here is the JSON:\n{"extractions": [{"person": "محمد"}]}\nDone.'),
            '{"extractions": [{"person": "محمد"}]}',
        )

    def test_preserves_unparseable_output_for_langextract_error(self) -> None:
        self.assertEqual(coerce_json_output("not json"), "not json")

    def test_normalizes_bare_extraction_dict(self) -> None:
        self.assertEqual(
            normalize_langextract_json('{"person": "محمد", "person_attributes": {}}'),
            '{"extractions": [{"person": "محمد", "person_attributes": {}}]}',
        )

    def test_normalizes_top_level_list(self) -> None:
        self.assertEqual(
            normalize_langextract_json('[{"fiqh_term": "الصيام"}]'),
            '{"extractions": [{"fiqh_term": "الصيام"}]}',
        )

    def test_normalizes_common_list_key(self) -> None:
        self.assertEqual(
            normalize_langextract_json('{"terms": [{"fiqh_term": "الصيام"}]}'),
            '{"extractions": [{"fiqh_term": "الصيام"}]}',
        )

    def test_validates_empty_extractions_shape(self) -> None:
        self.assertEqual(classify_langextract_json('{"extractions": []}'), ("success", ""))

    def test_validates_extraction_item_shape(self) -> None:
        self.assertEqual(
            classify_langextract_json('{"extractions": [{"person": "محمد", "person_attributes": {}}]}'),
            ("success", ""),
        )

    def test_rejects_non_scalar_extraction_text_shape(self) -> None:
        status, message = classify_langextract_json('{"extractions": [{"person": {"name": "محمد"}}]}')
        self.assertEqual(status, "schema_error")
        self.assertIn("person must be scalar extraction text", message)

    def test_normalized_common_key_is_valid_langextract_shape(self) -> None:
        normalized = normalize_langextract_json('{"items": [{"fiqh_term": "الصيام"}]}')
        self.assertEqual(classify_langextract_json(normalized), ("success", ""))

    def test_retry_prompt_keeps_repair_instruction(self) -> None:
        prompt = build_json_retry_prompt("Q: النص", "not json")
        self.assertIn('{"extractions": []}', prompt)
        self.assertIn("Previous invalid answer", prompt)

    def test_api_retry_recovers_transient_failure(self) -> None:
        model = _RetryModel(failures_before_success=1)
        output, api_retry_count = model._complete_with_retries("prompt", {})
        self.assertEqual(output, '{"extractions": []}')
        self.assertEqual(api_retry_count, 1)
        self.assertEqual(model.calls, 2)

    def test_api_retry_raises_after_limit(self) -> None:
        model = _RetryModel(failures_before_success=3)
        with self.assertRaises(exceptions.InferenceRuntimeError):
            model._complete_with_retries("prompt", {})
        self.assertEqual(model.calls, 3)

    def test_sha256_text_is_stable(self) -> None:
        self.assertEqual(sha256_text("abc"), sha256_text("abc"))
        self.assertNotEqual(sha256_text("abc"), sha256_text("abcd"))


if __name__ == "__main__":
    unittest.main()
