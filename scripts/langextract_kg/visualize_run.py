#!/usr/bin/env python3
"""Write LangExtract JSONL and HTML review files."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any
from uuid import UUID

import langextract as lx


def write_visualization(
    annotated_docs: list[Any],
    *,
    out_dir: Path,
    output_stem: str,
    generation: dict[str, str],
    show_progress: bool = False,
) -> tuple[Path, Path]:
    out_dir.mkdir(parents=True, exist_ok=True)
    jsonl_path = out_dir / f"{output_stem}.jsonl"
    html_path = out_dir / f"{output_stem}.html"
    lx.io.save_annotated_documents(
        annotated_docs,
        output_name=jsonl_path.name,
        output_dir=out_dir,
        show_progress=show_progress,
    )
    attach_machine_generation(jsonl_path, generation)
    html = lx.visualize(jsonl_path)
    html_text = html.data if hasattr(html, "data") else str(html)
    html_path.write_text(html_text, encoding="utf-8")
    return jsonl_path, html_path


def attach_machine_generation(jsonl_path: Path, generation: dict[str, str]) -> None:
    """Add the run descriptor to every raw LangExtract document atomically."""
    try:
        canonical_generation = {
            "run_id": str(UUID(str(generation["run_id"]).strip())),
            "model_id": str(generation["model_id"]).strip(),
            "prompt_version": str(generation["prompt_version"]).strip(),
        }
    except (KeyError, TypeError, ValueError) as err:
        raise ValueError("raw LangExtract JSONL requires a valid generation descriptor") from err

    if not canonical_generation["model_id"] or not canonical_generation["prompt_version"]:
        raise ValueError("raw LangExtract JSONL requires model_id and prompt_version")

    temp_path = jsonl_path.with_name(f".{jsonl_path.name}.generation.tmp")
    try:
        with jsonl_path.open("r", encoding="utf-8") as source, temp_path.open(
            "w", encoding="utf-8"
        ) as target:
            for line_number, line in enumerate(source, start=1):
                if not line.strip():
                    continue

                document = json.loads(line)
                existing = document.get("generation")
                if existing not in (None, canonical_generation):
                    raise ValueError(
                        f"raw LangExtract JSONL line {line_number} conflicts with generation descriptor"
                    )

                document["provenance_class"] = "machine"
                document["generation"] = dict(canonical_generation)
                target.write(json.dumps(document, ensure_ascii=False) + "\n")

        temp_path.replace(jsonl_path)
    except Exception:
        temp_path.unlink(missing_ok=True)
        raise


def main() -> int:
    args = parse_args()
    html = lx.visualize(args.langextract_jsonl)
    html_text = html.data if hasattr(html, "data") else str(html)
    output = Path(args.html).expanduser()
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(html_text, encoding="utf-8")
    print(f"html={output}")
    return 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--langextract-jsonl", required=True, help="LangExtract annotated JSONL file")
    parser.add_argument("--html", required=True, help="HTML output path")
    return parser.parse_args()


if __name__ == "__main__":
    raise SystemExit(main())
