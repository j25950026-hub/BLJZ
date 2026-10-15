#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

make_fixture() {
	local name="$1"
	local status="$2"
	local guardian_action="${3:-promote}"
	ROOT="$TMP_DIR/$name/root"
	STAGE="$TMP_DIR/$name/stage"
	UNIT_DIR="$TMP_DIR/$name/units"
	BIN_DIR="$TMP_DIR/$name/bin"
	LOG="$TMP_DIR/$name/systemctl.log"
	mkdir -p "$ROOT/releases/release-new" "$ROOT/data/recovery" "$STAGE" "$UNIT_DIR" "$BIN_DIR"
	printf 'legacy service\n' >"$UNIT_DIR/bilibili-ledgerbot.service"
	printf 'new service\n' >"$STAGE/bilibili-ledgerbot.service"
	printf 'guardian service\n' >"$STAGE/ledgerbot-guardian.service"
	printf 'guardian timer\n' >"$STAGE/ledgerbot-guardian.timer"
	printf '#!/usr/bin/env bash\nexit 0\n' >"$ROOT/ledgerbot"
	chmod 0755 "$ROOT/ledgerbot"
	if [[ "$guardian_action" == "promote" ]]; then
		guardian_action='ln -sfn "$PWD/releases/release-new" "$PWD/last-known-good"'
	else
		guardian_action='exit 0'
	fi
	printf '%s\n' \
		'#!/usr/bin/env bash' \
		"printf '%s\\n' \"\${LEDGERBOT_RELEASE_ROOT:-}\" >\"$ROOT/guardian-release-root.txt\"" \
		"$guardian_action" >"$ROOT/releases/release-new/ledgerbot-guardian"
	printf '#!/usr/bin/env bash\nexit 0\n' >"$ROOT/releases/release-new/ledgerbot"
	chmod 0755 "$ROOT/releases/release-new/ledgerbot" "$ROOT/releases/release-new/ledgerbot-guardian"
	ln -s "$ROOT/releases/release-new" "$ROOT/current"
	printf '%s\n' "$status" >"$ROOT/data/recovery/poll-status.json"
	printf '%s\n' '#!/usr/bin/env bash' "printf '%s\\n' \"\$*\" >>\"$LOG\"" 'if [[ "${1:-}" == "is-active" ]]; then' "  printf 'active\\n'" 'fi' >"$BIN_DIR/systemctl"
	printf '%s\n' '#!/usr/bin/env bash' "printf 'journal snapshot\\n'" >"$BIN_DIR/journalctl"
	chmod 0755 "$BIN_DIR/systemctl" "$BIN_DIR/journalctl"
}

RELEASE_SHA="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
make_fixture "success" "{\"release_id\":\"release-new\",\"release_sha\":\"$RELEASE_SHA\",\"consecutive_successes\":2}"
LEDGERBOT_RELEASE_ROOT="" SYSTEMCTL_BIN="$BIN_DIR/systemctl" JOURNALCTL_BIN="$BIN_DIR/journalctl" POLL_VERIFY_TIMEOUT_SECONDS=1 \
	bash "$PROJECT_DIR/scripts/activate_ledgerbot_guardian_release.sh" \
		--root "$ROOT" --stage "$STAGE" --unit-dir "$UNIT_DIR" \
		--release-id release-new --release-sha "$RELEASE_SHA"
grep -F 'new service' "$UNIT_DIR/bilibili-ledgerbot.service" >/dev/null
if ! grep -Fx "$ROOT" "$ROOT/guardian-release-root.txt" >/dev/null; then
	echo "activation must pass an absolute release root to Guardian" >&2
	exit 1
fi
[[ "$(readlink -f "$ROOT/last-known-good")" == "$ROOT/releases/release-new" ]]
grep -F 'daemon-reload' "$LOG" >/dev/null
grep -F 'restart bilibili-ledgerbot.service' "$LOG" >/dev/null
grep -F 'enable --now ledgerbot-guardian.timer' "$LOG" >/dev/null

make_fixture "promotion-failure" "{\"release_id\":\"release-new\",\"release_sha\":\"$RELEASE_SHA\",\"consecutive_successes\":2}" "skip"
if LEDGERBOT_RELEASE_ROOT="" SYSTEMCTL_BIN="$BIN_DIR/systemctl" JOURNALCTL_BIN="$BIN_DIR/journalctl" POLL_VERIFY_TIMEOUT_SECONDS=1 \
	bash "$PROJECT_DIR/scripts/activate_ledgerbot_guardian_release.sh" \
		--root "$ROOT" --stage "$STAGE" --unit-dir "$UNIT_DIR" \
		--release-id release-new --release-sha "$RELEASE_SHA"; then
	echo "failed last-known-good promotion must fail activation" >&2
	exit 1
fi
if ! grep -F 'legacy service' "$UNIT_DIR/bilibili-ledgerbot.service" >/dev/null; then
	echo "failed last-known-good promotion must restore the legacy service" >&2
	exit 1
fi
[[ ! -e "$ROOT/last-known-good" ]]
[[ "$(grep -F -c 'restart bilibili-ledgerbot.service' "$LOG")" == "2" ]]
if grep -F 'enable --now ledgerbot-guardian.timer' "$LOG" >/dev/null; then
	echo "failed last-known-good promotion must not enable Guardian" >&2
	exit 1
fi

make_fixture "failure" "{\"release_id\":\"release-new\",\"release_sha\":\"$RELEASE_SHA\",\"consecutive_successes\":1}"
if SYSTEMCTL_BIN="$BIN_DIR/systemctl" JOURNALCTL_BIN="$BIN_DIR/journalctl" POLL_VERIFY_TIMEOUT_SECONDS=1 \
	bash "$PROJECT_DIR/scripts/activate_ledgerbot_guardian_release.sh" \
		--root "$ROOT" --stage "$STAGE" --unit-dir "$UNIT_DIR" \
		--release-id release-new --release-sha "$RELEASE_SHA"; then
	echo "failed poll verification must fail activation" >&2
	exit 1
fi
grep -F 'legacy service' "$UNIT_DIR/bilibili-ledgerbot.service" >/dev/null
[[ ! -e "$ROOT/last-known-good" ]]
[[ "$(grep -F -c 'restart bilibili-ledgerbot.service' "$LOG")" == "2" ]]
if grep -F 'enable --now ledgerbot-guardian.timer' "$LOG" >/dev/null; then
	echo "failed poll verification must not enable Guardian" >&2
	exit 1
fi

echo "guardian activation tests passed"
