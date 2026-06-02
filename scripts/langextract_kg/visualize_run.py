#!/usr/bin/env python3
"""Write LangExtract JSONL and HTML review files."""

from __future__ import annotations

import argparse
from pathlib import Path
from typing import Any

import langextract as lx


def write_visualization(
    annotated_docs: list[Any],
    *,
    out_dir: Path,
    output_stem: str,
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
    html = lx.visualize(jsonl_path)
    html_text = html.data if hasattr(html, "data") else str(html)
    html_path.write_text(html_text, encoding="utf-8")
    return jsonl_path, html_path


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
