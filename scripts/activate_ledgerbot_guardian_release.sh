#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=""
STAGE=""
UNIT_DIR="/etc/systemd/system"
RELEASE_ID=""
RELEASE_SHA=""
SERVICE_NAME="bilibili-ledgerbot.service"
SYSTEMCTL_BIN="${SYSTEMCTL_BIN:-systemctl}"
JOURNALCTL_BIN="${JOURNALCTL_BIN:-journalctl}"
PYTHON_BIN="${PYTHON_BIN:-python3}"
POLL_VERIFY_TIMEOUT_SECONDS="${POLL_VERIFY_TIMEOUT_SECONDS:-180}"
ACTIVATION_STARTED=0
BACKUP_DIR=""
HAD_GUARDIAN_SERVICE=0
HAD_GUARDIAN_TIMER=0

usage() {
	printf '%s\n' \
		'Usage:' \
		'  scripts/activate_ledgerbot_guardian_release.sh --root ABSOLUTE_PATH --stage ABSOLUTE_PATH \' \
		'    --release-id ID --release-sha SHA256 [--unit-dir ABSOLUTE_PATH] [--service-name NAME]' \
		'' \
		'Activates a staged LedgerBot release. This command restarts the bot service only' \
		'after installing the staged unit templates and rolls back to the legacy flat' \
		'binary service if the new release does not complete two successful polls.'
}

fail() {
	echo "guardian activation: $*" >&2
	if [[ "$ACTIVATION_STARTED" == "1" ]]; then
		restore_legacy_service || true
		ACTIVATION_STARTED=0
	fi
	exit 1
}

while (($# > 0)); do
	case "$1" in
	--root)
		(($# >= 2)) || fail "--root requires a value"
		ROOT="$2"
		shift 2
		;;
	--stage)
		(($# >= 2)) || fail "--stage requires a value"
		STAGE="$2"
		shift 2
		;;
	--unit-dir)
		(($# >= 2)) || fail "--unit-dir requires a value"
		UNIT_DIR="$2"
		shift 2
		;;
	--release-id)
		(($# >= 2)) || fail "--release-id requires a value"
		RELEASE_ID="$2"
		shift 2
		;;
	--release-sha)
		(($# >= 2)) || fail "--release-sha requires a value"
		RELEASE_SHA="$2"
		shift 2
		;;
	--service-name)
		(($# >= 2)) || fail "--service-name requires a value"
		SERVICE_NAME="$2"
		shift 2
		;;
	-h|--help)
		usage
		exit 0
		;;
	*)
		fail "unknown argument: $1"
		;;
	esac
done

[[ "$ROOT" == /* && "$ROOT" != "/" ]] || fail "--root must be an absolute non-root path"
[[ "$STAGE" == /* ]] || fail "--stage must be an absolute path"
[[ "$UNIT_DIR" == /* ]] || fail "--unit-dir must be an absolute path"
[[ "$RELEASE_ID" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || fail "--release-id is invalid"
RELEASE_SHA="${RELEASE_SHA,,}"
[[ "$RELEASE_SHA" =~ ^[a-f0-9]{64}$ ]] || fail "--release-sha is invalid"
[[ "$POLL_VERIFY_TIMEOUT_SECONDS" =~ ^[1-9][0-9]*$ ]] || fail "POLL_VERIFY_TIMEOUT_SECONDS must be positive"

ROOT="$(cd "$ROOT" && pwd -P)"
STAGE="$(cd "$STAGE" && pwd -P)"
CURRENT_RELEASE="$(readlink -f "$ROOT/current")"
EXPECTED_RELEASE="$ROOT/releases/$RELEASE_ID"
[[ "$CURRENT_RELEASE" == "$EXPECTED_RELEASE" ]] || fail "current does not point to the requested release"
[[ -x "$CURRENT_RELEASE/ledgerbot" && -x "$CURRENT_RELEASE/ledgerbot-guardian" ]] || fail "current release binaries are missing"
for source in "$STAGE/bilibili-ledgerbot.service" "$STAGE/ledgerbot-guardian.service" "$STAGE/ledgerbot-guardian.timer"; do
	[[ -f "$source" ]] || fail "staged unit template missing: $source"
done

mkdir -p "$ROOT/rollback" "$ROOT/logs" "$UNIT_DIR"
STAMP="$(date -u '+%Y%m%d_%H%M%S')"
BACKUP_DIR="$ROOT/rollback/guardian-activation-$STAMP"
mkdir -p "$BACKUP_DIR"
LOG_FILE="$ROOT/logs/guardian_activation_$STAMP.log"
exec >>"$LOG_FILE" 2>&1

capture_journal() {
	local name="$1"
	"$JOURNALCTL_BIN" -u "$SERVICE_NAME" -n 240 --no-pager >"$BACKUP_DIR/$name" 2>&1 || true
}

restore_legacy_service() {
	if [[ "$ACTIVATION_STARTED" != "1" ]]; then
		return
	fi
	capture_journal "journal-after-failure.txt"
	if [[ -f "$BACKUP_DIR/bilibili-ledgerbot.service" ]]; then
		install -m 0644 "$BACKUP_DIR/bilibili-ledgerbot.service" "$UNIT_DIR/bilibili-ledgerbot.service"
	fi
	if [[ "$HAD_GUARDIAN_SERVICE" != "1" ]]; then
		rm -f -- "$UNIT_DIR/ledgerbot-guardian.service"
	fi
	if [[ "$HAD_GUARDIAN_TIMER" != "1" ]]; then
		rm -f -- "$UNIT_DIR/ledgerbot-guardian.timer"
	fi
	"$SYSTEMCTL_BIN" daemon-reload || true
	"$SYSTEMCTL_BIN" restart "$SERVICE_NAME" || true
}

on_error() {
	local status="$?"
	restore_legacy_service
	exit "$status"
}
trap on_error ERR

if [[ -f "$UNIT_DIR/bilibili-ledgerbot.service" ]]; then
	install -m 0644 "$UNIT_DIR/bilibili-ledgerbot.service" "$BACKUP_DIR/bilibili-ledgerbot.service"
else
	fail "existing bot service unit is missing"
fi
if [[ -f "$UNIT_DIR/ledgerbot-guardian.service" ]]; then
	HAD_GUARDIAN_SERVICE=1
	install -m 0644 "$UNIT_DIR/ledgerbot-guardian.service" "$BACKUP_DIR/ledgerbot-guardian.service"
fi
if [[ -f "$UNIT_DIR/ledgerbot-guardian.timer" ]]; then
	HAD_GUARDIAN_TIMER=1
	install -m 0644 "$UNIT_DIR/ledgerbot-guardian.timer" "$BACKUP_DIR/ledgerbot-guardian.timer"
fi
capture_journal "journal-before.txt"

ACTIVATION_STARTED=1
install -m 0644 "$STAGE/bilibili-ledgerbot.service" "$UNIT_DIR/bilibili-ledgerbot.service"
install -m 0644 "$STAGE/ledgerbot-guardian.service" "$UNIT_DIR/ledgerbot-guardian.service"
install -m 0644 "$STAGE/ledgerbot-guardian.timer" "$UNIT_DIR/ledgerbot-guardian.timer"
"$SYSTEMCTL_BIN" daemon-reload
"$SYSTEMCTL_BIN" restart "$SERVICE_NAME"
"$SYSTEMCTL_BIN" is-active --quiet "$SERVICE_NAME"

"$PYTHON_BIN" - "$ROOT/data/recovery/poll-status.json" "$RELEASE_ID" "$RELEASE_SHA" "$POLL_VERIFY_TIMEOUT_SECONDS" <<'PY'
import json
import os
import sys
import time

path, release_id, release_sha, timeout_raw = sys.argv[1:]
deadline = time.monotonic() + int(timeout_raw)
while time.monotonic() < deadline:
    try:
        with open(path, "r", encoding="utf-8") as handle:
            status = json.load(handle)
        if (
            status.get("release_id") == release_id
            and str(status.get("release_sha", "")).lower() == release_sha.lower()
            and int(status.get("consecutive_successes", 0)) >= 2
        ):
            sys.exit(0)
    except (OSError, ValueError, TypeError):
        pass
    time.sleep(3)
sys.exit(1)
PY

cd "$ROOT"
LEDGERBOT_RELEASE_ROOT="$ROOT" "$CURRENT_RELEASE/ledgerbot-guardian"
[[ "$(readlink -f "$ROOT/last-known-good")" == "$EXPECTED_RELEASE" ]] || fail "Guardian did not promote the release to last-known-good"
"$SYSTEMCTL_BIN" enable --now ledgerbot-guardian.timer
"$SYSTEMCTL_BIN" is-active --quiet ledgerbot-guardian.timer
capture_journal "journal-after-success.txt"
ACTIVATION_STARTED=0
printf 'Guardian activation completed: release=%s log=%s\n' "$RELEASE_ID" "$LOG_FILE"
