#!/usr/bin/env python3
"""LangExtract adapter backed exclusively by Surau's U-0 gateway."""

from __future__ import annotations

import concurrent.futures
import dataclasses
import hashlib
import json
import re
import threading
import time
from pathlib import Path
from typing import Any, Iterator, Sequence

from langextract.core import base_model
from langextract.core import data
from langextract.core import exceptions
from langextract.core import types as core_types

import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from surau_inference import InferenceClient, InferenceError  # noqa: E402


@dataclasses.dataclass(init=False)
class SurauInferenceJSONModel(base_model.BaseLanguageModel):
    """LangExtract JSON model with a provider/model pinned U-0 session."""

    task_key: str
    client: InferenceClient
    session_id: str
    policy_hash: str
    model_id: str
    temperature: float | None
    max_output_tokens: int | None
    max_workers: int
    max_json_retries: int
    max_api_retries: int
    api_retry_sleep_seconds: float
    format_type: data.FormatType
    audit_sink: list[dict[str, Any]] | None
    attributions: list[dict[str, Any]]
    _audit_lock: threading.Lock
    _request_counter: int

    def __init__(
        self,
        *,
        task_key: str,
        client: InferenceClient,
        session_id: str,
        policy_hash: str,
        temperature: float | None = None,
        max_output_tokens: int | None = None,
        max_workers: int = 1,
        max_json_retries: int = 2,
        max_api_retries: int = 0,
        api_retry_sleep_seconds: float = 1.0,
        audit_sink: list[dict[str, Any]] | None = None,
    ) -> None:
        super().__init__()
        if not task_key.startswith("langextract-"):
            raise exceptions.InferenceConfigError("invalid LangExtract inference task")
        if not session_id:
            raise exceptions.InferenceConfigError("U-0 batch session is required")
        if not re.fullmatch(r"[0-9a-f]{64}", policy_hash):
            raise exceptions.InferenceConfigError("frozen LangExtract policy hash is required")
        self.task_key = task_key
        self.client = client
        self.session_id = session_id
        self.policy_hash = policy_hash
        self.model_id = "u0-registry"
        self.temperature = temperature
        self.max_output_tokens = max_output_tokens
        self.max_workers = max(1, int(max_workers or 1))
        self.max_json_retries = max(0, int(max_json_retries or 0))
        self.max_api_retries = max(0, int(max_api_retries or 0))
        self.api_retry_sleep_seconds = max(0.0, float(api_retry_sleep_seconds or 0.0))
        self.format_type = data.FormatType.JSON
        self.audit_sink = audit_sink
        self.attributions = []
        self._audit_lock = threading.Lock()
        self._request_counter = 0

    @property
    def requires_fence_output(self) -> bool:
        return False

    def infer(
        self,
        batch_prompts: Sequence[str],
        **kwargs: Any,
    ) -> Iterator[Sequence[core_types.ScoredOutput]]:
        if self.max_workers <= 1 or len(batch_prompts) <= 1:
            for prompt in batch_prompts:
                yield [self._process_single_prompt(prompt, self._next_request_index())]
            return

        with concurrent.futures.ThreadPoolExecutor(max_workers=self.max_workers) as executor:
            futures = [
                executor.submit(self._process_single_prompt, prompt, self._next_request_index())
                for prompt in batch_prompts
            ]
            for future in futures:
                yield [future.result()]

    def _next_request_index(self) -> int:
        with self._audit_lock:
            request_index = self._request_counter
            self._request_counter += 1
        return request_index

    def _process_single_prompt(
        self,
        prompt: str,
        request_index: int,
    ) -> core_types.ScoredOutput:
        raw_output = ""
        current_prompt = prompt
        final_output = ""
        error_message = ""
        for attempt in range(self.max_json_retries + 1):
            try:
                result, api_retry_count = self._complete_with_retries(current_prompt)
            except exceptions.InferenceRuntimeError as err:
                self._record_audit(
                    request_index=request_index,
                    prompt=prompt,
                    raw_output=raw_output,
                    normalized_output=final_output,
                    parse_status="api_error",
                    retry_count=attempt,
                    api_retry_count=self.max_api_retries,
                    error_message=str(err),
                    attribution=None,
                )
                raise

            raw_output = str(result.get("output") or "")
            output = normalize_langextract_json(coerce_json_output(raw_output))
            final_output = output
            parse_status, validation_error = classify_langextract_json(output)
            if parse_status == "success":
                self._record_audit(
                    request_index=request_index,
                    prompt=prompt,
                    raw_output=raw_output,
                    normalized_output=final_output,
                    parse_status=parse_status,
                    retry_count=attempt,
                    api_retry_count=api_retry_count,
                    error_message="",
                    attribution=result,
                )
                return core_types.ScoredOutput(score=1.0, output=output)
            if attempt < self.max_json_retries:
                current_prompt = build_json_retry_prompt(prompt, raw_output)
            error_message = validation_error

        parse_status = "empty" if not raw_output.strip() else "schema_error"
        self._record_audit(
            request_index=request_index,
            prompt=prompt,
            raw_output=raw_output,
            normalized_output=final_output,
            parse_status=parse_status,
            retry_count=self.max_json_retries,
            api_retry_count=0,
            error_message=error_message or "empty model output",
            attribution=None,
        )
        return core_types.ScoredOutput(score=1.0, output=raw_output.strip())

    def _complete_with_retries(self, prompt: str) -> tuple[dict[str, Any], int]:
        last_error: exceptions.InferenceRuntimeError | None = None
        for attempt in range(self.max_api_retries + 1):
            try:
                return self._complete(prompt), attempt
            except exceptions.InferenceRuntimeError as err:
                last_error = err
                if attempt >= self.max_api_retries:
                    break
                if self.api_retry_sleep_seconds:
                    time.sleep(self.api_retry_sleep_seconds)
        assert last_error is not None
        raise last_error

    def _complete(self, prompt: str) -> dict[str, Any]:
        try:
            return self.client.invoke(
                self.task_key,
                prompt,
                session_id=self.session_id,
                cache_vary={
                    "langextract_task": self.task_key,
                    "policy_hash": self.policy_hash,
                },
                variables={"policy_hash": self.policy_hash},
            )
        except InferenceError as err:
            raise exceptions.InferenceRuntimeError(
                f"Surau inference gateway error ({err.code or 'unknown'}): {err}",
                original=err,
            ) from err

    def _record_audit(
        self,
        *,
        request_index: int,
        prompt: str,
        raw_output: str,
        normalized_output: str,
        parse_status: str,
        retry_count: int,
        api_retry_count: int,
        error_message: str,
        attribution: dict[str, Any] | None,
    ) -> None:
        safe_attribution = None
        if attribution is not None:
            safe_attribution = {
                key: attribution.get(key)
                for key in (
                    "call_id",
                    "generation",
                    "provider",
                    "model",
                    "prompt_version",
                    "response_schema_version",
                    "usage",
                    "cost",
                    "cache_status",
                    "failover",
                )
            }
        audit = {
            "request_index": request_index,
            "prompt_hash": sha256_text(prompt),
            "output_hash": sha256_text(raw_output),
            "normalized_output_hash": sha256_text(normalized_output),
            "parse_status": parse_status,
            "retry_count": retry_count,
            "api_retry_count": api_retry_count,
            "error_message": error_message,
            "raw_output": raw_output,
            "inference": safe_attribution,
        }
        with self._audit_lock:
            if safe_attribution is not None:
                self.attributions.append(safe_attribution)
            if self.audit_sink is not None:
                self.audit_sink.append(audit)


# One-release import alias; it no longer speaks to a provider.
OpenAICompatibleJSONModel = SurauInferenceJSONModel


_FENCE_RE = re.compile(r"```(?:json)?\s*(.*?)```", re.IGNORECASE | re.DOTALL)


def coerce_json_output(output: str) -> str:
    text = output.strip().lstrip("\ufeff")
    if _is_json(text):
        return text
    for match in _FENCE_RE.finditer(text):
        candidate = match.group(1).strip()
        if _is_json(candidate):
            return candidate
    candidate = _first_json_value(text)
    if candidate and _is_json(candidate):
        return candidate
    return text


def normalize_langextract_json(output: str) -> str:
    try:
        parsed = json.loads(output)
    except json.JSONDecodeError:
        return output.strip()
    if isinstance(parsed, dict):
        if isinstance(parsed.get("extractions"), list):
            return json.dumps(parsed, ensure_ascii=False)
        for key in ("items", "mentions", "entities", "terms", "citations", "results"):
            value = parsed.get(key)
            if isinstance(value, list):
                return json.dumps({"extractions": value}, ensure_ascii=False)
        return json.dumps({"extractions": [parsed]}, ensure_ascii=False)
    if isinstance(parsed, list):
        return json.dumps({"extractions": parsed}, ensure_ascii=False)
    return output.strip()


def classify_langextract_json(output: str) -> tuple[str, str]:
    if not output.strip():
        return "empty", "empty model output"
    try:
        parsed = json.loads(output)
    except json.JSONDecodeError as err:
        return "parse_error", f"invalid JSON: {err}"
    if not isinstance(parsed, dict):
        return "schema_error", "top-level JSON must be an object"
    unexpected_top_level_keys = sorted(key for key in parsed if key != data.EXTRACTIONS_KEY)
    if unexpected_top_level_keys:
        return "schema_error", f"unexpected top-level keys: {', '.join(unexpected_top_level_keys)}"
    extractions = parsed.get("extractions")
    if not isinstance(extractions, list):
        return "schema_error", "top-level object must contain an extractions list"
    for index, item in enumerate(extractions):
        error = _validate_extraction_item(item)
        if error:
            return "schema_error", f"extractions[{index}]: {error}"
    return "success", ""


def _validate_extraction_item(item: Any) -> str:
    if not isinstance(item, dict):
        return "item must be an object"
    extraction_keys = [key for key in item if isinstance(key, str) and not key.endswith(data.ATTRIBUTE_SUFFIX)]
    if len(extraction_keys) != 1:
        return "item must contain exactly one extraction text key"
    extraction_key = extraction_keys[0]
    extraction_value = item.get(extraction_key)
    if isinstance(extraction_value, bool) or not isinstance(extraction_value, (str, int, float)):
        return f"{extraction_key} must be scalar extraction text"
    attributes_key = f"{extraction_key}{data.ATTRIBUTE_SUFFIX}"
    allowed_keys = {extraction_key, attributes_key}
    unexpected_keys = sorted(str(key) for key in item if key not in allowed_keys)
    if unexpected_keys:
        return f"unexpected keys: {', '.join(unexpected_keys)}"
    if attributes_key in item and item[attributes_key] is not None and not isinstance(item[attributes_key], dict):
        return f"{attributes_key} must be an object or null"
    return ""


def build_json_retry_prompt(original_prompt: str, previous_output: str) -> str:
    clipped_output = previous_output.strip()
    if len(clipped_output) > 3000:
        clipped_output = clipped_output[:3000] + "\n...[truncated]"
    return (
        "The previous answer was not valid LangExtract JSON.\n"
        "Return only raw JSON with this exact top-level shape:\n"
        '{"extractions": []}\n'
        "When there are extractions, each item must follow the examples in the original prompt, "
        'for example {"fiqh_term": "الصيام", "fiqh_term_attributes": {}}.\n'
        "Do not include Markdown, prose, analysis, or keys outside the JSON object.\n\n"
        "Original prompt:\n"
        f"{original_prompt}\n\n"
        "Previous invalid answer:\n"
        f"{clipped_output}\n"
    )


def _is_json(value: str) -> bool:
    try:
        json.loads(value)
    except json.JSONDecodeError:
        return False
    return True


def sha256_text(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def _first_json_value(text: str) -> str:
    start = -1
    opener = ""
    for index, char in enumerate(text):
        if char in "{[":
            start = index
            opener = char
            break
    if start < 0:
        return ""
    stack = [opener]
    in_string = False
    escape = False
    pairs = {"{": "}", "[": "]"}
    for index in range(start + 1, len(text)):
        char = text[index]
        if in_string:
            if escape:
                escape = False
            elif char == "\\":
                escape = True
            elif char == '"':
                in_string = False
            continue
        if char == '"':
            in_string = True
            continue
        if char in "{[":
            stack.append(char)
            continue
        if char in "}]":
            if not stack or pairs[stack[-1]] != char:
                return ""
            stack.pop()
            if not stack:
                return text[start : index + 1].strip()
    return ""
