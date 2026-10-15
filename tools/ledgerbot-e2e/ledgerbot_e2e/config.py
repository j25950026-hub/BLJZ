from __future__ import annotations

import os
from pathlib import Path

from .models import Config


def load_env_file(path: str | Path = ".env") -> dict[str, str]:
    env_path = Path(path)
    values: dict[str, str] = {}
    if not env_path.exists():
        return values
    for raw_line in env_path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip().strip('"').strip("'")
    return values


def load_config(path: str | Path = ".env") -> Config:
    raw = dict(os.environ)
    raw.update(load_env_file(path))
    return load_config_from_mapping(raw)


def load_config_from_mapping(raw: dict[str, str]) -> Config:
    required = ("API_ID", "API_HASH", "BOT_USERNAME", "TEST_GROUPS")
    missing = [key for key in required if not str(raw.get(key, "")).strip()]
    if missing:
        raise ValueError(f"missing required config: {', '.join(missing)}")

    try:
        api_id = int(str(raw["API_ID"]).strip())
    except ValueError as exc:
        raise ValueError("API_ID must be an integer") from exc

    test_groups = {
        item.strip()
        for item in str(raw["TEST_GROUPS"]).split(",")
        if item.strip()
    }
    if not test_groups:
        raise ValueError("TEST_GROUPS must contain at least one target")

    max_concurrency = _positive_int(raw.get("MAX_CONCURRENCY", "3"), "MAX_CONCURRENCY")
    min_delay_sec = _non_negative_int(raw.get("MIN_DELAY_SEC", "10"), "MIN_DELAY_SEC")
    max_delay_sec = _non_negative_int(raw.get("MAX_DELAY_SEC", "30"), "MAX_DELAY_SEC")
    if max_delay_sec < min_delay_sec:
        raise ValueError("MAX_DELAY_SEC must be greater than or equal to MIN_DELAY_SEC")

    return Config(
        api_id=api_id,
        api_hash=str(raw["API_HASH"]).strip(),
        bot_username=str(raw["BOT_USERNAME"]).strip().lstrip("@"),
        test_groups=test_groups,
        max_concurrency=max_concurrency,
        min_delay_sec=min_delay_sec,
        max_delay_sec=max_delay_sec,
        report_dir=str(raw.get("REPORT_DIR", "reports")).strip() or "reports",
        session_dir=str(raw.get("SESSION_DIR", "sessions")).strip() or "sessions",
    )


def _positive_int(value: object, name: str) -> int:
    parsed = _non_negative_int(value, name)
    if parsed <= 0:
        raise ValueError(f"{name} must be greater than 0")
    return parsed


def _non_negative_int(value: object, name: str) -> int:
    try:
        parsed = int(str(value).strip())
    except ValueError as exc:
        raise ValueError(f"{name} must be an integer") from exc
    if parsed < 0:
        raise ValueError(f"{name} must be non-negative")
    return parsed

