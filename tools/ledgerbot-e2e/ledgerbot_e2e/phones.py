from __future__ import annotations

from pathlib import Path

import yaml


def load_phones(path: str | Path = "phones.local.yml") -> dict[str, str]:
    phone_path = Path(path)
    if not phone_path.exists():
        return {}
    payload = yaml.safe_load(phone_path.read_text(encoding="utf-8")) or {}
    rows = payload.get("phones")
    if rows is None:
        return {}
    if not isinstance(rows, list):
        raise ValueError("phones must be a list")

    out: dict[str, str] = {}
    for index, row in enumerate(rows, start=1):
        if not isinstance(row, dict):
            raise ValueError(f"phone row #{index} must be an object")
        account = str(row.get("account", "")).strip()
        phone = str(row.get("phone", "")).strip()
        if not account or not phone:
            raise ValueError(f"phone row #{index} requires account and phone")
        if account in out:
            raise ValueError(f"duplicate phone account: {account}")
        out[account] = phone
    return out

