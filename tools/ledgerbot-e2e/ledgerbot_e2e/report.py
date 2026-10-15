from __future__ import annotations

import json
import re
from collections import Counter
from pathlib import Path
from typing import Iterable

from .models import StepResult

BOT_TOKEN_RE = re.compile(r"\b\d{6,}:[A-Za-z0-9_-]{20,}\b")
SESSION_PATH_RE = re.compile(r"[\w./-]+\.session(?:-journal)?")
API_HASH_RE = re.compile(r"\b[a-fA-F0-9]{32}\b")
CARD_LIKE_RE = re.compile(r"\b\d{13,19}\b")


def mask_sensitive_text(text: str) -> str:
    masked = BOT_TOKEN_RE.sub("[bot-token]", text)
    masked = API_HASH_RE.sub("[api-hash]", masked)
    masked = SESSION_PATH_RE.sub("[session-path]", masked)
    masked = CARD_LIKE_RE.sub("[number]", masked)
    return masked


def write_jsonl(path: str | Path, rows: Iterable[StepResult]) -> None:
    out_path = Path(path)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    with out_path.open("a", encoding="utf-8") as fh:
        for row in rows:
            payload = row.to_json_dict()
            payload["send"] = mask_sensitive_text(str(payload.get("send", "")))
            payload["actual"] = mask_sensitive_text(str(payload.get("actual", "")))
            fh.write(json.dumps(payload, ensure_ascii=False) + "\n")


def summarize_results(rows: list[dict[str, object]]) -> dict[str, object]:
    total = len(rows)
    passed = sum(1 for row in rows if row.get("status") == "pass")
    failed = total - passed
    errors = Counter(
        str(row.get("error_type") or "unknown")
        for row in rows
        if row.get("status") != "pass"
    )
    return {
        "total": total,
        "passed": passed,
        "failed": failed,
        "errors": dict(errors),
    }

