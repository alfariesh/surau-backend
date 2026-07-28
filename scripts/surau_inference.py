"""Small stdlib client for the metered U-0 inference gateway.

Generator scripts receive provider/model/prompt/run attribution from the
gateway response. They never read a provider credential or construct a
provider URL themselves.
"""

from __future__ import annotations

import json
import os
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Any


class InferenceError(RuntimeError):
    """Stable gateway failure with optional retry/reset metadata."""

    def __init__(self, message: str, *, code: str = "", retry_after: int = 0) -> None:
        super().__init__(message)
        self.code = code
        self.retry_after = retry_after


RESUMABLE_EXIT_CODE = 75


def budget_exceeded(error: BaseException) -> InferenceError | None:
    """Find a U-0 cap rejection through SDK/wrapper exception chains."""
    current: BaseException | None = error
    seen: set[int] = set()
    while current is not None and id(current) not in seen:
        seen.add(id(current))
        if isinstance(current, InferenceError) and current.code == "inference_budget_exceeded":
            return current
        original = getattr(current, "original", None)
        if isinstance(original, BaseException):
            current = original
            continue
        current = current.__cause__ or current.__context__
    return None


@dataclass(frozen=True)
class InferenceClient:
    base_url: str
    service_token: str
    timeout_seconds: int = 180

    @classmethod
    def from_env(cls, base_url: str | None = None, timeout_seconds: int = 180) -> "InferenceClient":
        resolved_url = (
            base_url
            or os.environ.get("SURAU_INFERENCE_BASE_URL")
            or os.environ.get("SURAU_API_BASE_URL")
            or "http://127.0.0.1:8080"
        )
        token = os.environ.get("SURAU_INFERENCE_SERVICE_TOKEN", "").strip()
        if not token:
            raise InferenceError(
                "SURAU_INFERENCE_SERVICE_TOKEN is required for metered generation",
                code="inference_service_token_missing",
            )
        return cls(resolved_url.rstrip("/"), token, timeout_seconds)

    def invoke(
        self,
        task_key: str,
        user: str,
        *,
        session_id: str = "",
        cache_vary: dict[str, Any] | None = None,
        source_version: str = "",
        index_version: str = "",
        variables: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        rendered_variables: dict[str, Any] = {"user": user}
        rendered_variables.update(variables or {})
        payload: dict[str, Any] = {
            "task_key": task_key,
            "variables": rendered_variables,
            "cache_vary": cache_vary or {},
        }
        if session_id:
            payload["session_id"] = session_id
        if source_version:
            payload["source_version"] = source_version
        if index_version:
            payload["index_version"] = index_version
        return self._request("/internal/inference/invoke", payload)

    def create_session(self, task_key: str) -> dict[str, Any]:
        return self._request("/internal/inference/sessions", {"task_key": task_key})

    def _request(self, path: str, payload: dict[str, Any]) -> dict[str, Any]:
        body = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode()
        request = urllib.request.Request(
            self.base_url + path,
            data=body,
            method="POST",
            headers={
                "Content-Type": "application/json",
                "Accept": "application/json",
                "X-Internal-Token": self.service_token,
            },
        )
        try:
            with urllib.request.urlopen(request, timeout=self.timeout_seconds) as response:
                parsed = json.loads(response.read().decode())
        except urllib.error.HTTPError as err:
            parsed = _error_payload(err)
            raise InferenceError(
                str(parsed.get("message") or parsed.get("error") or f"inference HTTP {err.code}"),
                code=str(parsed.get("code") or ""),
                retry_after=int(parsed.get("retry_after") or 0),
            ) from err
        except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as err:
            raise InferenceError(f"inference gateway unavailable: {err}") from err
        if not isinstance(parsed, dict):
            raise InferenceError("inference gateway returned a non-object response")
        return parsed


def generation_identity(result: dict[str, Any]) -> dict[str, str]:
    generation = result.get("generation")
    if not isinstance(generation, dict):
        raise InferenceError("inference response is missing generation identity")
    identity = {
        "run_id": str(generation.get("run_id") or ""),
        "model_id": str(generation.get("model_id") or ""),
        "prompt_version": str(generation.get("prompt_version") or ""),
    }
    if not all(identity.values()):
        raise InferenceError("inference response has an incomplete generation identity")
    return identity


def parse_output(result: dict[str, Any]) -> dict[str, Any]:
    raw = result.get("output")
    if not isinstance(raw, str):
        raise InferenceError("inference response is missing output")
    try:
        parsed = json.loads(raw)
    except json.JSONDecodeError as err:
        raise InferenceError("inference output is not valid JSON") from err
    if not isinstance(parsed, dict):
        raise InferenceError("inference output is not a JSON object")
    return parsed


def attribution_metadata(result: dict[str, Any]) -> dict[str, Any]:
    return {
        "provider": result.get("provider"),
        "model": result.get("model"),
        "prompt_version": result.get("prompt_version"),
        "response_schema_version": result.get("response_schema_version"),
        "inference_call_id": result.get("call_id"),
        "usage": result.get("usage"),
        "cost": result.get("cost"),
        "cache_status": result.get("cache_status"),
        "failover": bool(result.get("failover")),
    }


def _error_payload(err: urllib.error.HTTPError) -> dict[str, Any]:
    try:
        parsed = json.loads(err.read().decode())
    except (json.JSONDecodeError, UnicodeDecodeError):
        return {}
    return parsed if isinstance(parsed, dict) else {}
