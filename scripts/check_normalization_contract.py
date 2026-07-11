#!/usr/bin/env python3
"""Reject semantic edits to frozen search-key profiles against a PR base."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import re
import subprocess
import sys

ACTIVE_GO = "internal/quranutil/normalize.go"
ACTIVE_PYTHON = "scripts/langextract_kg/search_key.py"


def versioned_paths(version: int) -> tuple[str, ...]:
    return (
        f"internal/quranutil/normalize_v{version}.go",
        f"internal/quranutil/normalization_v{version}_vectors.json",
        f"internal/quranutil/normalization_v{version}_unicode_ranges.json",
        f"internal/quranutil/normalization_v{version}_unicode_ranges_gen.go",
        f"scripts/langextract_kg/search_key_v{version}.py",
    )


def _git(repo: Path, *args: str, check: bool = True) -> subprocess.CompletedProcess[bytes]:
    return subprocess.run(
        ("git", *args),
        cwd=repo,
        check=check,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )


def _read_at(repo: Path, ref: str, path: str) -> bytes | None:
    result = _git(repo, "show", f"{ref}:{path}", check=False)
    return result.stdout if result.returncode == 0 else None


def _profile_version(content: bytes | None) -> int | None:
    if content is None:
        return None
    match = re.search(rb"\bProfileVersion\s*=\s*(\d+)\b", content)
    return int(match.group(1)) if match else None


def _validate_selector(content: bytes | None, version: int, runtime: str) -> bool:
    if content is None:
        return False
    if runtime == "go":
        pattern = rb"return\s+NormalizeKeyV" + str(version).encode() + rb"\(value\)"
    else:
        pattern = rb"from\s+\.search_key_v" + str(version).encode() + rb"\s+import"
    return re.search(pattern, content) is not None


def check_contract(repo: Path, base_ref: str, head_ref: str = "HEAD") -> list[str]:
    """Return contract violations between the merge base and candidate head."""
    merge_head = "HEAD" if head_ref == "WORKTREE" else head_ref
    merge_base = _git(repo, "merge-base", base_ref, merge_head).stdout.decode().strip()

    def read_head(path: str) -> bytes | None:
        if head_ref != "WORKTREE":
            return _read_at(repo, head_ref, path)
        candidate = repo / path
        return candidate.read_bytes() if candidate.is_file() else None

    base_presence = [_read_at(repo, merge_base, path) is not None for path in versioned_paths(1)]
    errors: list[str] = []

    head_go = read_head(ACTIVE_GO)
    head_python = read_head(ACTIVE_PYTHON)
    head_version = _profile_version(head_go)

    # The B-5 pull request establishes the baseline. Subsequent pull requests
    # always take the stricter branch below and compare bytes with merge-base.
    if not any(base_presence):
        if head_version != 1:
            errors.append("initial normalization baseline must select ProfileVersion 1")
        for path in versioned_paths(1):
            if read_head(path) is None:
                errors.append(f"initial normalization baseline is missing {path}")
        if not _validate_selector(head_go, 1, "go"):
            errors.append("Go NormalizeKey does not select NormalizeKeyV1")
        if not _validate_selector(head_python, 1, "python"):
            errors.append("Python normalized_key does not select search_key_v1")
        return errors

    if not all(base_presence):
        return ["base branch has an incomplete search-key/v1 semantic baseline"]

    base_go = _read_at(repo, merge_base, ACTIVE_GO)
    base_python = _read_at(repo, merge_base, ACTIVE_PYTHON)
    base_version = _profile_version(base_go)
    if base_version is None or head_version is None:
        return ["cannot read numeric ProfileVersion from the active Go selector"]

    # Every version already present on the base is byte-immutable. Updating a
    # checksum in the same PR cannot bypass this comparison.
    for version in range(1, base_version + 1):
        for path in versioned_paths(version):
            before = _read_at(repo, merge_base, path)
            after = read_head(path)
            if before is None:
                errors.append(f"base profile v{version} is missing {path}")
            elif after != before:
                errors.append(f"frozen normalization artifact changed: {path}")

    active_changed = head_go != base_go or head_python != base_python
    if not active_changed:
        if head_version != base_version:
            errors.append("ProfileVersion changed without changing the active selectors")
        return errors

    if head_version != base_version + 1:
        errors.append(
            "active normalization changed without incrementing ProfileVersion by exactly one"
        )
        return errors

    for path in versioned_paths(head_version):
        if _read_at(repo, merge_base, path) is not None:
            errors.append(f"new profile path already exists on the base: {path}")
        if read_head(path) is None:
            errors.append(f"new profile v{head_version} is missing {path}")
    if not _validate_selector(head_go, head_version, "go"):
        errors.append(f"Go NormalizeKey does not select NormalizeKeyV{head_version}")
    if not _validate_selector(head_python, head_version, "python"):
        errors.append(f"Python normalized_key does not select search_key_v{head_version}")
    return errors


def _default_base() -> str:
    if value := os.environ.get("NORMALIZATION_BASE_REF"):
        return value
    if value := os.environ.get("GITHUB_BASE_REF"):
        return f"origin/{value}"
    return "origin/main"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default=_default_base())
    parser.add_argument("--head", default="WORKTREE")
    args = parser.parse_args()

    repo = Path(__file__).resolve().parents[1]
    try:
        errors = check_contract(repo, args.base, args.head)
    except subprocess.CalledProcessError as error:
        sys.stderr.write(error.stderr.decode(errors="replace"))
        return 2
    if errors:
        for error in errors:
            print(f"normalization-contract: {error}", file=sys.stderr)
        return 1
    print(f"normalization-contract: immutable profiles match merge-base {args.base}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
