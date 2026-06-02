#!/usr/bin/env python3
"""OpenAI-compatible LangExtract model with GLM/SumoPod-friendly fallbacks."""

from __future__ import annotations

import concurrent.futures
import dataclasses
import hashlib
import json
import re
import threading
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
        self.format_type = data.FormatType.JSON
        self.audit_sink = audit_sink
        self._audit_lock = threading.Lock()
        self._request_counter = 0
        self._client = OpenAI(api_key=api_key, base_url=base_url)

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
        error_message = ""
        for attempt in range(self.max_json_retries + 1):
            retry_count = attempt
            raw_output = self._complete(current_prompt, config)
            output = normalize_langextract_json(coerce_json_output(raw_output))
            final_output = output
            if _is_json(output):
                parse_status = "success"
                self._record_audit(
                    request_index=request_index,
                    prompt=prompt,
                    raw_output=raw_output,
                    normalized_output=final_output,
                    parse_status=parse_status,
                    retry_count=retry_count,
                    error_message="",
                )
                return core_types.ScoredOutput(score=1.0, output=output)
            if attempt < self.max_json_retries:
                current_prompt = build_json_retry_prompt(prompt, raw_output)

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
            error_message=error_message,
        )
        return core_types.ScoredOutput(score=1.0, output=raw_output.strip())

    def _record_audit(
        self,
        *,
        request_index: int,
        prompt: str,
        raw_output: str,
        normalized_output: str,
        parse_status: str,
        retry_count: int,
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
            "error_message": error_message,
            "raw_output": raw_output,
        }
        with self._audit_lock:
            self.audit_sink.append(audit)

    def _complete(self, prompt: str, config: dict[str, Any]) -> str:
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

        try:
            response = self._client.chat.completions.create(**params)
            output = extract_message_text(response)
            if not output:
                raise exceptions.InferenceRuntimeError("OpenAI-compatible response contained no content")
            return output
        except exceptions.InferenceRuntimeError:
            raise
        except Exception as err:
            raise exceptions.InferenceRuntimeError(
                f"OpenAI-compatible API error: {err}",
                original=err,
            ) from err


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
