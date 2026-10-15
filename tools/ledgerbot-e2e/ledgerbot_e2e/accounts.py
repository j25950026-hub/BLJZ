from __future__ import annotations

from pathlib import Path

import yaml

from .models import Account


def load_accounts(path: str | Path = "accounts.yml") -> dict[str, Account]:
    account_path = Path(path)
    if not account_path.exists():
        raise FileNotFoundError(f"accounts file not found: {account_path}")
    payload = yaml.safe_load(account_path.read_text(encoding="utf-8")) or {}
    rows = payload.get("accounts")
    if not isinstance(rows, list) or not rows:
        raise ValueError("accounts must be a non-empty list")

    out: dict[str, Account] = {}
    for index, row in enumerate(rows, start=1):
        if not isinstance(row, dict):
            raise ValueError(f"account #{index} must be an object")
        name = str(row.get("name", "")).strip()
        role = str(row.get("role", "")).strip()
        session = str(row.get("session", "")).strip()
        if not name or not role or not session:
            raise ValueError(f"account #{index} requires name, role, and session")
        if name in out:
            raise ValueError(f"duplicate account name: {name}")
        out[name] = Account(name=name, role=role, session=session)
    return out

