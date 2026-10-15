from __future__ import annotations

import argparse
from pathlib import Path

from .config import load_config
from .runner import validate_dry_run
from .suite_loader import load_suite

ALLOWED_STEP_FIELDS = {
    "name",
    "account",
    "send",
    "expect_any",
    "expect_none",
    "timeout_sec",
    "append_run_id",
}


def validate_generated_suite(path: str | Path, accounts_path: str = "accounts.yml", config_path: str = ".env") -> None:
    cfg = load_config(config_path)
    suite = load_suite(path)
    validate_dry_run(suite, accounts_path, cfg.test_groups, cfg.bot_username)


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate AI-generated LedgerBot E2E suites.")
    sub = parser.add_subparsers(dest="command", required=True)
    validate_parser = sub.add_parser("validate")
    validate_parser.add_argument("suite")
    validate_parser.add_argument("--accounts", default="accounts.yml")
    validate_parser.add_argument("--config", default=".env")
    args = parser.parse_args()

    if args.command == "validate":
        validate_generated_suite(args.suite, args.accounts, args.config)
        print(f"validation ok: {args.suite}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

