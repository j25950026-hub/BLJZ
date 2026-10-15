#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=""
RELEASE_ID=""
LEDGERBOT=""
GUARDIAN=""
EXPECTED_LEDGERBOT_SHA=""
DRY_RUN=0
STAGING_DIR=""

usage() {
	cat <<'EOF'
Usage:
  scripts/deploy_release_layout.sh --root ABSOLUTE_PATH --release-id ID \
    --ledgerbot PATH --guardian PATH [--expected-ledgerbot-sha SHA256] [--dry-run]

Creates an immutable release directory and atomically switches the current link.
This command never restarts a service and never marks a release rollback eligible.
EOF
}

fail() {
	echo "release layout: $*" >&2
	exit 1
}

cleanup() {
	if [[ -n "$STAGING_DIR" && -d "$STAGING_DIR" ]]; then
		rm -rf -- "$STAGING_DIR"
	fi
}
trap cleanup EXIT

while (($# > 0)); do
	case "$1" in
	--root)
		(($# >= 2)) || fail "--root requires a value"
		ROOT="$2"
		shift 2
		;;
	--release-id)
		(($# >= 2)) || fail "--release-id requires a value"
		RELEASE_ID="$2"
		shift 2
		;;
	--ledgerbot)
		(($# >= 2)) || fail "--ledgerbot requires a value"
		LEDGERBOT="$2"
		shift 2
		;;
	--guardian)
		(($# >= 2)) || fail "--guardian requires a value"
		GUARDIAN="$2"
		shift 2
		;;
	--expected-ledgerbot-sha)
		(($# >= 2)) || fail "--expected-ledgerbot-sha requires a value"
		EXPECTED_LEDGERBOT_SHA="$2"
		shift 2
		;;
	--dry-run)
		DRY_RUN=1
		shift
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

[[ -n "$ROOT" ]] || fail "--root is required"
[[ "$ROOT" == /* && "$ROOT" != "/" ]] || fail "--root must be an absolute non-root path"
[[ "$RELEASE_ID" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || fail "--release-id must contain only letters, numbers, dots, underscores, or hyphens"
[[ -f "$LEDGERBOT" ]] || fail "ledgerbot artifact is not a regular file"
[[ -f "$GUARDIAN" ]] || fail "guardian artifact is not a regular file"

LEDGERBOT_SHA="$(sha256sum "$LEDGERBOT" | awk '{print $1}')"
GUARDIAN_SHA="$(sha256sum "$GUARDIAN" | awk '{print $1}')"
if [[ -n "$EXPECTED_LEDGERBOT_SHA" ]]; then
	EXPECTED_LEDGERBOT_SHA="${EXPECTED_LEDGERBOT_SHA,,}"
	[[ "$EXPECTED_LEDGERBOT_SHA" =~ ^[a-f0-9]{64}$ ]] || fail "expected ledgerbot SHA256 is invalid"
	[[ "$LEDGERBOT_SHA" == "$EXPECTED_LEDGERBOT_SHA" ]] || fail "ledgerbot SHA256 does not match the expected value"
fi

if ((DRY_RUN)); then
	printf 'root=%s\nrelease_id=%s\nledgerbot_sha256=%s\nguardian_sha256=%s\n' "$ROOT" "$RELEASE_ID" "$LEDGERBOT_SHA" "$GUARDIAN_SHA"
	exit 0
fi

umask 027
mkdir -p "$ROOT/releases"
ROOT="$(cd "$ROOT" && pwd -P)"
RELEASES_DIR="$ROOT/releases"
chmod 0755 "$RELEASES_DIR"
RELEASE_DIR="$RELEASES_DIR/$RELEASE_ID"
[[ ! -e "$RELEASE_DIR" && ! -L "$RELEASE_DIR" ]] || fail "release already exists: $RELEASE_ID"

STAGING_DIR="$(mktemp -d "$RELEASES_DIR/.${RELEASE_ID}.stage.XXXXXX")"
install -m 0755 "$LEDGERBOT" "$STAGING_DIR/ledgerbot"
install -m 0755 "$GUARDIAN" "$STAGING_DIR/ledgerbot-guardian"

INSTALLED_LEDGERBOT_SHA="$(sha256sum "$STAGING_DIR/ledgerbot" | awk '{print $1}')"
INSTALLED_GUARDIAN_SHA="$(sha256sum "$STAGING_DIR/ledgerbot-guardian" | awk '{print $1}')"
[[ "$INSTALLED_LEDGERBOT_SHA" == "$LEDGERBOT_SHA" ]] || fail "installed ledgerbot checksum mismatch"
[[ "$INSTALLED_GUARDIAN_SHA" == "$GUARDIAN_SHA" ]] || fail "installed guardian checksum mismatch"

CREATED_AT="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
printf '{"version":1,"release_id":"%s","ledgerbot_sha256":"%s","guardian_sha256":"%s","created_at":"%s","rollback_eligible":false}\n' \
	"$RELEASE_ID" "$LEDGERBOT_SHA" "$GUARDIAN_SHA" "$CREATED_AT" >"$STAGING_DIR/manifest.json"
printf 'RELEASE_ID=%s\nRELEASE_SHA256=%s\n' "$RELEASE_ID" "$LEDGERBOT_SHA" >"$STAGING_DIR/release.env"
chmod 0640 "$STAGING_DIR/manifest.json" "$STAGING_DIR/release.env"
chmod 0755 "$STAGING_DIR"

mv "$STAGING_DIR" "$RELEASE_DIR"
STAGING_DIR=""
CURRENT_NEXT="$ROOT/current.next.$$"
ln -s "$RELEASE_DIR" "$CURRENT_NEXT"
mv -Tf "$CURRENT_NEXT" "$ROOT/current"

printf 'installed_release=%s\nledgerbot_sha256=%s\nguardian_sha256=%s\n' "$RELEASE_ID" "$LEDGERBOT_SHA" "$GUARDIAN_SHA"
