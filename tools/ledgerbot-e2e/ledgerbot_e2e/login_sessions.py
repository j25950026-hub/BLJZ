from __future__ import annotations

import argparse
import asyncio
from pathlib import Path

from .accounts import load_accounts
from .config import load_config
from .phones import load_phones
from .telegram_client import TelegramClientAdapter


def main() -> int:
    parser = argparse.ArgumentParser(description="Login Telegram user sessions for LedgerBot E2E.")
    parser.add_argument("--account", help="Account name from accounts.yml, e.g. user01")
    parser.add_argument("--all", action="store_true", help="Login all accounts from accounts.yml")
    parser.add_argument("--config", default=".env")
    parser.add_argument("--accounts", default="accounts.yml")
    parser.add_argument("--phones", default="phones.local.yml", help="Local account-to-phone YAML file")
    parser.add_argument("--start-at", help="Start batch login at this account name")
    parser.add_argument("--limit", type=int, help="Maximum number of accounts to process")
    parser.add_argument("--include-existing", action="store_true", help="Do not skip already authorized sessions")
    parser.add_argument("--status-files", action="store_true", help="Only show whether session files exist; no Telegram network")
    args = parser.parse_args()

    if args.status_files and not args.all and not args.account:
        args.all = True

    if not args.all and not args.account:
        parser.error("use --account user01 or --all")
    if args.all and args.account:
        parser.error("use either --account or --all, not both")

    cfg = load_config(args.config)
    accounts = load_accounts(args.accounts)
    phones = load_phones(args.phones)
    adapter = TelegramClientAdapter(cfg, accounts)
    targets = list(accounts) if args.all else [args.account]
    if args.start_at:
        if args.start_at not in targets:
            parser.error(f"--start-at account not selected: {args.start_at}")
        targets = targets[targets.index(args.start_at) :]
    if args.limit is not None:
        targets = targets[: args.limit]

    Path(cfg.session_dir).mkdir(parents=True, exist_ok=True)
    if args.status_files:
        for name in targets:
            exists = Path(accounts[name].session).exists()
            print(f"{name}: {'present' if exists else 'missing'} {accounts[name].session}")
        return 0

    for name in targets:
        phone = phones.get(name)
        phone_hint = "phone loaded" if phone else "phone prompt required"
        print(f"[{name}] login check; {phone_hint}; enter code/2FA locally when prompted")
        try:
            message = asyncio.run(
                adapter.login_account(
                    name,
                    phone=phone,
                    skip_authorized=not args.include_existing,
                )
            )
        except Exception as exc:  # noqa: BLE001 - batch login should continue after one account fails.
            print(f"[{name}] failed: {exc}")
            continue
        print(f"[{name}] {message}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
