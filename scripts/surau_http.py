"""Scoped HTTP identity helper for Surau-owned enrichment requests only."""

from __future__ import annotations

import os
from pathlib import Path
from urllib.parse import urlsplit


def service_token() -> str:
    """Load the current job token without ever printing or caching it."""

    token_file = os.environ.get("SURAU_ENRICHMENT_SERVICE_TOKEN_FILE", "").strip()
    if token_file:
        return Path(token_file).read_text(encoding="utf-8").strip()
    return os.environ.get("SURAU_ENRICHMENT_SERVICE_TOKEN", "").strip()


def surau_headers(base_url: str, request_url: str, headers: dict[str, str] | None = None) -> dict[str, str]:
    """Attach identity only when the destination origin is exactly Surau.

    This origin check is the hard boundary preventing a Surau credential from
    riding along to DeepSeek, Sumopod, or another configured LLM provider.
    """

    base = urlsplit(base_url)
    target = urlsplit(request_url)
    if (base.scheme.casefold(), base.hostname, base.port) != (
        target.scheme.casefold(),
        target.hostname,
        target.port,
    ):
        raise ValueError("refusing to attach Surau service token to a different origin")

    result = dict(headers or {})
    token = service_token()
    if token:
        result["X-Internal-Token"] = token
    return result
