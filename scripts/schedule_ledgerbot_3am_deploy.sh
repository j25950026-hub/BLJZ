#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SSH_KEY="${SSH_KEY:-/home/eren/桌面/shuai/deploy/ssh/orderflow_43_156_119_108_rsa}"
REMOTE="${REMOTE:-root@43.156.119.108}"
REMOTE_DIR="${REMOTE_DIR:-/opt/bilibili-ledgerbot-20260704}"
SERVICE="${SERVICE:-bilibili-ledgerbot.service}"
REMOTE_HEALTH_URL="${REMOTE_HEALTH_URL:-http://127.0.0.1:18080/healthz}"
REMOTE_READY_URL="${REMOTE_READY_URL:-http://127.0.0.1:18080/readyz}"
TARGET_TZ="${TARGET_TZ:-Asia/Shanghai}"

CONFIRM=0
SKIP_TESTS=0
ARTIFACT=""
WHEN=""

usage() {
	cat <<'EOF'
Usage:
  scripts/schedule_ledgerbot_3am_deploy.sh [--confirm] [--artifact PATH] [--when "YYYY-MM-DD HH:MM:SS"] [--skip-tests]

Defaults:
  --when is the next 03:00:00 in Asia/Shanghai.
  Without --confirm, this script only prints the deployment plan.

Environment overrides:
  SSH_KEY, REMOTE, REMOTE_DIR, SERVICE, TARGET_TZ, REMOTE_HEALTH_URL, REMOTE_READY_URL
EOF
}

fail() {
	printf 'ERROR: %s\n' "$*" >&2
	exit 1
}

info() {
	printf '%s\n' "$*"
}

next_three_am() {
	local now_epoch target_epoch
	now_epoch="$(TZ="$TARGET_TZ" date +%s)"
	target_epoch="$(TZ="$TARGET_TZ" date -d "today 03:00:00" +%s)"
	if ((target_epoch <= now_epoch)); then
		target_epoch="$(TZ="$TARGET_TZ" date -d "tomorrow 03:00:00" +%s)"
	fi
	TZ="$TARGET_TZ" date -d "@$target_epoch" "+%Y-%m-%d %H:%M:%S"
}

while (($# > 0)); do
	case "$1" in
		--confirm)
			CONFIRM=1
			shift
			;;
		--skip-tests)
			SKIP_TESTS=1
			shift
			;;
		--artifact)
			[[ $# -ge 2 ]] || fail "--artifact requires a path"
			ARTIFACT="$2"
			shift 2
			;;
		--when)
			[[ $# -ge 2 ]] || fail "--when requires a date time"
			WHEN="$2"
			shift 2
			;;
		-h | --help)
			usage
			exit 0
			;;
		*)
			fail "unknown argument: $1"
			;;
	esac
done

cd "$PROJECT_DIR"

command -v ssh >/dev/null 2>&1 || fail "ssh not found"
command -v scp >/dev/null 2>&1 || fail "scp not found"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum not found"
[[ -f "$SSH_KEY" ]] || fail "SSH key not found: $SSH_KEY"

if [[ -z "$WHEN" ]]; then
	WHEN="$(next_three_am)"
fi

TARGET_STAMP="$(TZ="$TARGET_TZ" date -d "$WHEN" "+%Y%m%d_%H%M%S")"
LOCAL_BIN="${ARTIFACT:-/tmp/ledgerbot-release-${TARGET_STAMP}}"
REMOTE_STAGE="$REMOTE_DIR/ledgerbot.next_${TARGET_STAMP}"
REMOTE_SCRIPT="$REMOTE_DIR/deploy_scheduled_${TARGET_STAMP}.sh"
REMOTE_LOG="$REMOTE_DIR/logs/deploy_scheduled_${TARGET_STAMP}.log"
SYSTEMD_UNIT="ledgerbot-deploy-${TARGET_STAMP}"

info "Scheduled deployment plan"
info "  project:       $PROJECT_DIR"
info "  target time:   $WHEN $TARGET_TZ"
info "  remote:        $REMOTE"
info "  remote dir:    $REMOTE_DIR"
info "  service:       $SERVICE"
info "  local binary:  $LOCAL_BIN"
info "  remote stage:  $REMOTE_STAGE"
info "  remote script: $REMOTE_SCRIPT"
info "  remote log:    $REMOTE_LOG"

if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	if ! git diff --quiet || ! git diff --cached --quiet || [[ -n "$(git ls-files --others --exclude-standard)" ]]; then
		info "WARNING: working tree has local changes; a freshly built artifact includes the current checkout."
	fi
fi

if ((CONFIRM == 0)); then
	info ""
	info "Dry-run only. Re-run with --confirm to build/upload/schedule the deployment at the target time."
	exit 0
fi

if [[ -z "$ARTIFACT" ]]; then
	if ((SKIP_TESTS == 0)); then
		info "Running tests..."
		go test ./internal/utils ./internal/bot
	else
		info "Skipping tests by request."
	fi
	info "Building release binary..."
	go build -o "$LOCAL_BIN" ./cmd/ledgerbot
else
	[[ -f "$LOCAL_BIN" ]] || fail "artifact not found: $LOCAL_BIN"
fi

chmod 0755 "$LOCAL_BIN"
LOCAL_SHA="$(sha256sum "$LOCAL_BIN" | awk '{print $1}')"
info "Local SHA256: $LOCAL_SHA"

TMP_REMOTE_SCRIPT="$(mktemp)"
cleanup() {
	rm -f "$TMP_REMOTE_SCRIPT"
}
trap cleanup EXIT

cat >"$TMP_REMOTE_SCRIPT" <<EOF
#!/usr/bin/env bash
set -Eeuo pipefail

REMOTE_DIR="$REMOTE_DIR"
SERVICE="$SERVICE"
STAGED="$REMOTE_STAGE"
EXPECTED_SHA="$LOCAL_SHA"
HEALTH_URL="$REMOTE_HEALTH_URL"
READY_URL="$REMOTE_READY_URL"
LOG_FILE="$REMOTE_LOG"

wait_for_url() {
	local url="\$1"
	local name="\$2"
	local attempt
	for attempt in \$(seq 1 60); do
		if curl -fsS "\$url"; then
			echo
			echo "\$name ok after \${attempt}s"
			return 0
		fi
		sleep 1
	done
	echo "\$name failed after 60s: \$url"
	return 1
}

mkdir -p "\$(dirname "\$LOG_FILE")"
{
	echo "===== scheduled deploy started: \$(date '+%Y-%m-%d %H:%M:%S %z') ====="
	cd "\$REMOTE_DIR"
	test -f "\$STAGED"
	actual_sha="\$(sha256sum "\$STAGED" | awk '{print \$1}')"
	echo "expected_sha=\$EXPECTED_SHA"
	echo "actual_sha=\$actual_sha"
	if [[ "\$actual_sha" != "\$EXPECTED_SHA" ]]; then
		echo "SHA256 mismatch, aborting."
		exit 1
	fi

	chmod 0755 "\$STAGED"
	backup="\$REMOTE_DIR/ledgerbot.bak_\$(date '+%Y%m%d_%H%M%S')_before_signed_division_calculator"
	cp -a "\$REMOTE_DIR/ledgerbot" "\$backup"
	echo "backup=\$backup"

	mv -f "\$STAGED" "\$REMOTE_DIR/ledgerbot"
	systemctl restart "\$SERVICE"
	systemctl is-active "\$SERVICE"
	wait_for_url "\$HEALTH_URL" "healthz"
	wait_for_url "\$READY_URL" "readyz"
	journalctl -u "\$SERVICE" -n 80 --no-pager
	echo "===== scheduled deploy finished: \$(date '+%Y-%m-%d %H:%M:%S %z') ====="
} >>"\$LOG_FILE" 2>&1
EOF

chmod 0755 "$TMP_REMOTE_SCRIPT"

info "Uploading staged binary and remote deploy script..."
scp -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new "$LOCAL_BIN" "$REMOTE:$REMOTE_STAGE"
scp -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new "$TMP_REMOTE_SCRIPT" "$REMOTE:$REMOTE_SCRIPT"

info "Verifying staged SHA256 on remote..."
REMOTE_SHA="$(ssh -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new "$REMOTE" "sha256sum '$REMOTE_STAGE' | cut -d ' ' -f1")"
[[ "$REMOTE_SHA" == "$LOCAL_SHA" ]] || fail "remote SHA256 mismatch: $REMOTE_SHA != $LOCAL_SHA"

if ssh -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new "$REMOTE" \
	"systemctl list-timers --all '${SYSTEMD_UNIT}.timer' --no-legend --no-pager | grep -q '${SYSTEMD_UNIT}.timer'"; then
	info "Existing systemd timer found; staged binary and deploy script updated."
else
	info "Creating systemd timer..."
	ssh -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new "$REMOTE" \
		"chmod 0755 '$REMOTE_SCRIPT' && systemd-run --unit='$SYSTEMD_UNIT' --on-calendar='$WHEN' --property=Type=oneshot /bin/bash '$REMOTE_SCRIPT'"
fi

info "Timer status:"
ssh -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new "$REMOTE" \
	"systemctl list-timers --all '${SYSTEMD_UNIT}.timer' --no-pager || true"

info "Scheduled. Check log after deployment:"
info "  ssh -i \"$SSH_KEY\" \"$REMOTE\" \"tail -120 '$REMOTE_LOG'\""
