#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GUARDIAN_SERVICE="$PROJECT_DIR/scripts/ledgerbot-guardian.service"
GUARDIAN_TIMER="$PROJECT_DIR/scripts/ledgerbot-guardian.timer"
BOT_SERVICE="$PROJECT_DIR/scripts/bilibili-ledgerbot.service.v2"

require_line() {
	local file="$1"
	local expected="$2"
	grep -F -- "$expected" "$file" >/dev/null || {
		echo "missing $expected in $file" >&2
		exit 1
	}
}

require_line "$GUARDIAN_SERVICE" "Type=oneshot"
require_line "$GUARDIAN_SERVICE" "User=root"
require_line "$GUARDIAN_SERVICE" "Group=ledgerbot"
require_line "$GUARDIAN_SERVICE" "Environment=LEDGERBOT_RELEASE_ROOT=/opt/bilibili-ledgerbot-20260704"
require_line "$GUARDIAN_SERVICE" "EnvironmentFile=/opt/bilibili-ledgerbot-20260704/.env"
require_line "$GUARDIAN_SERVICE" "EnvironmentFile=-/opt/bilibili-ledgerbot-20260704/current/release.env"
require_line "$GUARDIAN_SERVICE" "ExecStart=/usr/bin/flock -n -E 0 /run/lock/ledgerbot-guardian.lock /opt/bilibili-ledgerbot-20260704/current/ledgerbot-guardian"
require_line "$GUARDIAN_SERVICE" "NoNewPrivileges=true"
require_line "$GUARDIAN_SERVICE" "ProtectSystem=full"
require_line "$GUARDIAN_SERVICE" "ReadWritePaths=/opt/bilibili-ledgerbot-20260704/data/recovery /opt/bilibili-ledgerbot-20260704"
require_line "$GUARDIAN_TIMER" "OnBootSec=45s"
require_line "$GUARDIAN_TIMER" "OnUnitActiveSec=30s"
require_line "$GUARDIAN_TIMER" "Persistent=true"
require_line "$GUARDIAN_TIMER" "Unit=ledgerbot-guardian.service"
require_line "$BOT_SERVICE" "EnvironmentFile=-/opt/bilibili-ledgerbot-20260704/current/release.env"
require_line "$BOT_SERVICE" "ExecStart=/opt/bilibili-ledgerbot-20260704/current/ledgerbot"
require_line "$BOT_SERVICE" "/opt/bilibili-ledgerbot-20260704/data/recovery"

echo "guardian unit tests passed"
