#!/usr/bin/env python3
"""Shared generation identity contract for persisted machine enrichment."""

from __future__ import annotations

from typing import Any
from uuid import UUID, uuid4


MACHINE_PROVENANCE_CLASS = "machine"
READER_TRANSLATION_PROMPT_VERSION = "reader-translation-v1"
READER_SUMMARY_PROMPT_VERSION = "reader-summary-v1"
READER_SUMMARY_TRANSLATION_PROMPT_VERSION = "reader-summary-translation-v1"
CATALOG_TRANSLATION_PROMPT_VERSION = "catalog-translation-v1"


def new_generation_identity(model_id: str, prompt_version: str) -> dict[str, str]:
    """Create one immutable identity to be reused by a prompt family in a run."""
    model_id = str(model_id or "").strip()
    prompt_version = str(prompt_version or "").strip()
    if not model_id:
        raise ValueError("model_id is required for machine generation")
    if not prompt_version:
        raise ValueError("prompt_version is required for machine generation")
    return {
        "run_id": str(uuid4()),
        "model_id": model_id,
        "prompt_version": prompt_version,
    }


def parse_generation_identity(value: Any) -> tuple[tuple[str, str, str] | None, list[tuple[str, str]]]:
    """Validate a JSON identity and return its normalized tuple plus QA errors."""
    if not isinstance(value, dict):
        return None, [("MISSING_GENERATION", "generation must be a JSON object")]

    errors: list[tuple[str, str]] = []
    run_id = value.get("run_id")
    model_id = value.get("model_id")
    prompt_version = value.get("prompt_version")

    if not isinstance(run_id, str) or not run_id.strip():
        errors.append(("MISSING_GENERATION_RUN_ID", "generation.run_id is required"))
    else:
        run_id = run_id.strip()
        try:
            run_id = str(UUID(run_id))
        except ValueError:
            errors.append(("INVALID_GENERATION_RUN_ID", "generation.run_id must be a valid UUID"))

    if not isinstance(model_id, str) or not model_id.strip():
        errors.append(("MISSING_GENERATION_MODEL_ID", "generation.model_id is required"))
    else:
        model_id = model_id.strip()

    if not isinstance(prompt_version, str) or not prompt_version.strip():
        errors.append(("MISSING_GENERATION_PROMPT_VERSION", "generation.prompt_version is required"))
    else:
        prompt_version = prompt_version.strip()

    if errors:
        return None, errors
    return (run_id, model_id, prompt_version), []
