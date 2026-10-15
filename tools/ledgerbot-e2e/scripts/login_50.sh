#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [[ ! -f ".env" ]]; then
  echo "missing .env; copy config.example.env to .env and fill API_ID/API_HASH first" >&2
  exit 1
fi

if [[ ! -f "accounts.yml" ]]; then
  echo "missing accounts.yml; copy accounts.example.yml to accounts.yml first" >&2
  exit 1
fi

if [[ ! -f "phones.local.yml" ]]; then
  echo "phones.local.yml not found; phone numbers will be prompted by Telethon" >&2
fi

python -m ledgerbot_e2e.login_sessions --all "$@"

