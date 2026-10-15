# LedgerBot E2E

This tool is for testing owned Telegram accounts, owned test groups, and the configured LedgerBot only.

Safety rules:

1. Never commit `.env`, `accounts.yml`, `sessions/`, `reports/`, Telegram codes, API hashes, bot tokens, admin tokens, or session files.
2. Start with 1 account, then 5, 15, 30, and finally 50 accounts.
3. Keep `MAX_CONCURRENCY` low. The default is `3`.
4. Do not use real accounts to pressure-test Telegram. Use protocol/HTTP load tests for pressure.
5. If Telegram returns FloodWait, wait. Do not bypass it.

## Setup

```bash
cd "tools/ledgerbot-e2e"
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
cp config.example.env .env
cp accounts.example.yml accounts.yml
cp phones.local.example.yml phones.local.yml
```

Edit `.env` with your Telegram API credentials:

```env
API_ID=
API_HASH=
BOT_USERNAME=BL66888_bot
TEST_GROUPS=e2e-main
MAX_CONCURRENCY=3
MIN_DELAY_SEC=10
MAX_DELAY_SEC=30
REPORT_DIR=reports
SESSION_DIR=sessions
```

## Login Sessions

Login one account:

```bash
python -m ledgerbot_e2e.login_sessions --account user01
```

Login all accounts listed in `accounts.yml`:

```bash
python -m ledgerbot_e2e.login_sessions --all
```

Or use the batch helper:

```bash
./scripts/login_50.sh
```

Useful batch options:

```bash
./scripts/login_50.sh --status-files
./scripts/login_50.sh --start-at user21
./scripts/login_50.sh --limit 5
./scripts/login_50.sh --start-at user21 --limit 10
```

The script prompts for phone/code/2FA locally. Do not paste codes into chat. Session files are written under `sessions/` and are ignored by git.

`phones.local.yml` is optional but recommended for 50 accounts. It lets the script pre-fill each phone number so you only input Telegram codes and 2FA locally.

## Dry Run

Validate config, accounts, suite, and targets without connecting to Telegram:

```bash
python -m ledgerbot_e2e.runner --suite suites/ledger_core.yml --dry-run
```

## Real E2E Run

Run a small smoke suite first:

```bash
python -m ledgerbot_e2e.runner --suite suites/private_smoke.yml
```

Then expand gradually:

```bash
python -m ledgerbot_e2e.runner --suite suites/ledger_core.yml --max-concurrency 3
```

Reports are written to `reports/<run_id>.jsonl`.
