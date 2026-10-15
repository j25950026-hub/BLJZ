from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

from .models import Step, StepDefaults, Suite


def load_suite(path: str | Path) -> Suite:
    suite_path = Path(path)
    payload = yaml.safe_load(suite_path.read_text(encoding="utf-8")) or {}
    if not isinstance(payload, dict):
        raise ValueError("suite file must contain an object")

    name = str(payload.get("suite", "")).strip()
    target = str(payload.get("target", "")).strip()
    if not name:
        raise ValueError("suite is required")
    if not target:
        raise ValueError("target is required")

    defaults_payload = payload.get("defaults") or {}
    if not isinstance(defaults_payload, dict):
        raise ValueError("defaults must be an object")
    defaults = StepDefaults(
        timeout_sec=int(defaults_payload.get("timeout_sec", 20)),
        append_run_id=bool(defaults_payload.get("append_run_id", True)),
    )

    raw_steps = payload.get("steps")
    if not isinstance(raw_steps, list) or not raw_steps:
        raise ValueError("steps must be a non-empty list")

    steps = [_parse_step(row, index) for index, row in enumerate(raw_steps, start=1)]
    return Suite(name=name, target=target, defaults=defaults, steps=steps)


def _parse_step(row: Any, index: int) -> Step:
    if not isinstance(row, dict):
        raise ValueError(f"step #{index} must be an object")
    name = str(row.get("name", "")).strip()
    account = str(row.get("account", "")).strip()
    send = str(row.get("send", "")).strip()
    if not name or not account or not send:
        raise ValueError(f"step #{index} requires name, account, and send")

    expect_any = row.get("expect_any") or []
    expect_none = row.get("expect_none") or []
    if not isinstance(expect_any, list) or not isinstance(expect_none, list):
        raise ValueError(f"step #{index} expect_any/expect_none must be lists")
    if not expect_any and not expect_none:
        raise ValueError(f"step #{index} requires expect_any or expect_none")

    timeout_sec = row.get("timeout_sec")
    append = row.get("append_run_id")
    return Step(
        name=name,
        account=account,
        send=send,
        expect_any=[str(item) for item in expect_any],
        expect_none=[str(item) for item in expect_none],
        timeout_sec=int(timeout_sec) if timeout_sec is not None else None,
        append_run_id=bool(append) if append is not None else None,
    )

