#!/usr/bin/env python3
"""OpenAI-compatible LangExtract model with GLM/SumoPod-friendly fallbacks."""

from __future__ import annotations

import concurrent.futures
import dataclasses
import hashlib
import json
import multiprocessing as mp
import queue
import re
import threading
import time
from typing import Any, Iterator, Sequence

from langextract.core import base_model
from langextract.core import data
from langextract.core import exceptions
from langextract.core import types as core_types


@dataclasses.dataclass(init=False)
class OpenAICompatibleJSONModel(base_model.BaseLanguageModel):
    """Small OpenAI-compatible model adapter for LangExtract JSON extraction.

    The built-in LangExtract OpenAI provider only reads `message.content`.
    Some OpenAI-compatible GLM endpoints may place useful text in
    `message.reasoning_content`, or may benefit from provider-specific fields
    such as `thinking: disabled`. This adapter keeps that behavior local to the
    extraction script without patching `temp-langextract`.
    """

    model_id: str
    api_key: str
    base_url: str
    temperature: float | None
    max_output_tokens: int | None
    max_workers: int
    disable_thinking: bool
    max_json_retries: int
    max_api_retries: int
    api_retry_sleep_seconds: float
    request_timeout_seconds: float
    hard_request_timeout: bool
    format_type: data.FormatType
    audit_sink: list[dict[str, Any]] | None
    _client: Any = dataclasses.field(default=None, repr=False, compare=False)
    _audit_lock: threading.Lock = dataclasses.field(default_factory=threading.Lock, repr=False, compare=False)
    _request_counter: int = dataclasses.field(default=0, repr=False, compare=False)

    def __init__(
        self,
        *,
        model_id: str,
        api_key: str,
        base_url: str,
        temperature: float | None = None,
        max_output_tokens: int | None = None,
        max_workers: int = 1,
        disable_thinking: bool = True,
        max_json_retries: int = 2,
        max_api_retries: int = 2,
        api_retry_sleep_seconds: float = 1.0,
        request_timeout_seconds: float = 180.0,
        hard_request_timeout: bool = True,
        audit_sink: list[dict[str, Any]] | None = None,
    ) -> None:
        super().__init__()
        try:
            from openai import OpenAI
        except ImportError as err:
            raise exceptions.InferenceConfigError("openai package is required") from err
        if not api_key:
            raise exceptions.InferenceConfigError("API key not provided.")

        self.model_id = model_id
        self.api_key = api_key
        self.base_url = base_url
        self.temperature = temperature
        self.max_output_tokens = max_output_tokens
        self.max_workers = max(1, int(max_workers or 1))
        self.disable_thinking = disable_thinking
        self.max_json_retries = max(0, int(max_json_retries or 0))
        self.max_api_retries = max(0, int(max_api_retries or 0))
        self.api_retry_sleep_seconds = max(0.0, float(api_retry_sleep_seconds or 0.0))
        self.request_timeout_seconds = max(1.0, float(request_timeout_seconds or 180.0))
        self.hard_request_timeout = bool(hard_request_timeout)
        self.format_type = data.FormatType.JSON
        self.audit_sink = audit_sink
        self._audit_lock = threading.Lock()
        self._request_counter = 0
        self._client = OpenAI(api_key=api_key, base_url=base_url, timeout=self.request_timeout_seconds)

    @property
    def requires_fence_output(self) -> bool:
        return False

    def infer(
        self,
        batch_prompts: Sequence[str],
        **kwargs: Any,
    ) -> Iterator[Sequence[core_types.ScoredOutput]]:
        config = self._runtime_config(kwargs)
        if self.max_workers <= 1 or len(batch_prompts) <= 1:
            for prompt in batch_prompts:
                yield [self._process_single_prompt(prompt, config, self._next_request_index())]
            return

        with concurrent.futures.ThreadPoolExecutor(max_workers=self.max_workers) as executor:
            futures = [
                executor.submit(self._process_single_prompt, prompt, config, self._next_request_index())
                for prompt in batch_prompts
            ]
            for future in futures:
                yield [future.result()]

    def _next_request_index(self) -> int:
        with self._audit_lock:
            request_index = self._request_counter
            self._request_counter += 1
        return request_index

    def _runtime_config(self, kwargs: dict[str, Any]) -> dict[str, Any]:
        temp = kwargs.get("temperature", self.temperature)
        max_output_tokens = kwargs.get("max_output_tokens", self.max_output_tokens)
        return {
            "temperature": temp,
            "max_output_tokens": max_output_tokens,
            "top_p": kwargs.get("top_p"),
            "response_format": kwargs.get("response_format") or {"type": "json_object"},
        }

    def _process_single_prompt(
        self,
        prompt: str,
        config: dict[str, Any],
        request_index: int,
    ) -> core_types.ScoredOutput:
        raw_output = ""
        current_prompt = prompt
        final_output = ""
        parse_status = "parse_error"
        retry_count = 0
        api_retry_count = 0
        error_message = ""
        for attempt in range(self.max_json_retries + 1):
            retry_count = attempt
            try:
                raw_output, api_retry_count = self._complete_with_retries(current_prompt, config)
            except exceptions.InferenceRuntimeError as err:
                final_output = '{"extractions": []}'
                self._record_audit(
                    request_index=request_index,
                    prompt=prompt,
                    raw_output=raw_output,
                    normalized_output=final_output,
                    parse_status="api_error",
                    retry_count=retry_count,
                    api_retry_count=api_retry_count,
                    error_message=str(err),
                )
                return core_types.ScoredOutput(score=1.0, output=final_output)
            output = normalize_langextract_json(coerce_json_output(raw_output))
            final_output = output
            parse_status, validation_error = classify_langextract_json(output)
            if parse_status == "success":
                parse_status = "success"
                self._record_audit(
                    request_index=request_index,
                    prompt=prompt,
                    raw_output=raw_output,
                    normalized_output=final_output,
                    parse_status=parse_status,
                    retry_count=retry_count,
                    api_retry_count=api_retry_count,
                    error_message="",
                )
                return core_types.ScoredOutput(score=1.0, output=output)
            if attempt < self.max_json_retries:
                current_prompt = build_json_retry_prompt(prompt, raw_output)
            error_message = validation_error

        if not raw_output.strip():
            parse_status = "empty"
            error_message = "empty model output"
        self._record_audit(
            request_index=request_index,
            prompt=prompt,
            raw_output=raw_output,
            normalized_output=final_output,
            parse_status=parse_status,
            retry_count=retry_count,
            api_retry_count=api_retry_count,
            error_message=error_message,
        )
        return core_types.ScoredOutput(score=1.0, output=raw_output.strip())

    def _complete_with_retries(self, prompt: str, config: dict[str, Any]) -> tuple[str, int]:
        last_error: exceptions.InferenceRuntimeError | None = None
        for api_attempt in range(self.max_api_retries + 1):
            try:
                return self._complete(prompt, config), api_attempt
            except exceptions.InferenceRuntimeError as err:
                last_error = err
                if api_attempt >= self.max_api_retries:
                    break
                if self.api_retry_sleep_seconds:
                    time.sleep(self.api_retry_sleep_seconds)
        assert last_error is not None
        raise last_error

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
    ) -> None:
        if self.audit_sink is None:
            return
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
        }
        with self._audit_lock:
            self.audit_sink.append(audit)

    def _complete(self, prompt: str, config: dict[str, Any]) -> str:
        params = self._completion_params(prompt, config)
        try:
            if self.hard_request_timeout:
                return self._complete_with_process_timeout(params)
            return self._complete_with_client(params)
        except exceptions.InferenceRuntimeError:
            raise
        except Exception as err:
            raise exceptions.InferenceRuntimeError(
                f"OpenAI-compatible API error: {err}",
                original=err,
            ) from err

    def _completion_params(self, prompt: str, config: dict[str, Any]) -> dict[str, Any]:
        params: dict[str, Any] = {
            "model": self.model_id,
            "messages": [
                {
                    "role": "system",
                    "content": (
                        "You are a careful extraction engine. Return only raw JSON, "
                        "with no Markdown fences, no prose, and no commentary."
                    ),
                },
                {"role": "user", "content": prompt},
            ],
            "n": 1,
            "response_format": config["response_format"],
        }
        if config.get("temperature") is not None:
            params["temperature"] = config["temperature"]
        if config.get("max_output_tokens") is not None:
            params["max_tokens"] = config["max_output_tokens"]
        if config.get("top_p") is not None:
            params["top_p"] = config["top_p"]
        if self.disable_thinking:
            params["extra_body"] = {"thinking": {"type": "disabled"}}
        return params

    def _complete_with_client(self, params: dict[str, Any]) -> str:
        response = self._client.chat.completions.create(**params, timeout=self.request_timeout_seconds)
        return extract_message_text(response)

    def _complete_with_process_timeout(self, params: dict[str, Any]) -> str:
        ctx = mp.get_context("spawn")
        result_queue = ctx.Queue(maxsize=1)
        process = ctx.Process(
            target=_openai_completion_worker,
            args=(
                result_queue,
                self.api_key,
                self.base_url,
                self.request_timeout_seconds,
                params,
            ),
        )
        process.daemon = True
        process.start()
        try:
            process.join(self.request_timeout_seconds)
            if process.is_alive():
                process.terminate()
                process.join(5)
                raise exceptions.InferenceRuntimeError(
                    f"OpenAI-compatible API timeout after {self.request_timeout_seconds:g}s"
                )
            try:
                result = result_queue.get(timeout=1)
            except queue.Empty as err:
                raise exceptions.InferenceRuntimeError(
                    f"OpenAI-compatible API worker exited without output; exitcode={process.exitcode}"
                ) from err
        finally:
            result_queue.close()
            result_queue.join_thread()

        if result.get("ok"):
            return str(result.get("output") or "")
        raise exceptions.InferenceRuntimeError(str(result.get("error") or "OpenAI-compatible API error"))


def _openai_completion_worker(
    result_queue: Any,
    api_key: str,
    base_url: str,
    timeout_seconds: float,
    params: dict[str, Any],
) -> None:
    try:
        from openai import OpenAI

        client = OpenAI(api_key=api_key, base_url=base_url, timeout=timeout_seconds)
        response = client.chat.completions.create(**params, timeout=timeout_seconds)
        result_queue.put({"ok": True, "output": extract_message_text(response)})
    except Exception as err:  # pragma: no cover - exercised by live provider smoke tests.
        result_queue.put({"ok": False, "error": f"{type(err).__name__}: {err}"})


def extract_message_text(response: Any) -> str:
    """Return content from OpenAI SDK response, including reasoning fallback."""
    try:
        message = response.choices[0].message
    except Exception:
        return ""

    for attr in ("content", "reasoning_content"):
        value = getattr(message, attr, None)
        if isinstance(value, str) and value.strip():
            return value.strip()

    if hasattr(message, "model_dump"):
        dumped = message.model_dump()
        for key in ("content", "reasoning_content"):
            value = dumped.get(key)
            if isinstance(value, str) and value.strip():
                return value.strip()
    if isinstance(message, dict):
        for key in ("content", "reasoning_content"):
            value = message.get(key)
            if isinstance(value, str) and value.strip():
                return value.strip()
    return ""


_FENCE_RE = re.compile(r"```(?:json)?\s*(.*?)```", re.IGNORECASE | re.DOTALL)


def coerce_json_output(output: str) -> str:
    """Return a parseable JSON object/array from a chat-completion response.

    OpenAI-compatible providers occasionally ignore `response_format` enough to
    add Markdown fences or a short natural-language prefix. LangExtract expects
    raw JSON at parse time, so this keeps the provider quirk contained here.
    """
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
    """Normalize valid JSON into LangExtract's wrapper object shape."""
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
    """Classify normalized LangExtract JSON as success, parse_error, or schema_error."""
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
    """Ask the model to repair a non-JSON answer without changing evidence."""
    clipped_output = previous_output.strip()
    if len(clipped_output) > 3000:
        clipped_output = clipped_output[:3000] + "\n...[truncated]"
    return (
        "The previous answer was not valid LangExtract JSON.\n"
        "Return only raw JSON with this exact top-level shape:\n"
        '{"extractions": []}\n'
        "When there are extractions, each item must follow the examples in the original prompt, "
        "for example {\"fiqh_term\": \"الصيام\", \"fiqh_term_attributes\": {}}.\n"
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
