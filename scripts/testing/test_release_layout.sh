#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

ROOT="$TMP_DIR/releases-root"
OLD_RELEASE="$ROOT/releases/release-old"
LEDGERBOT_ARTIFACT="$TMP_DIR/ledgerbot"
GUARDIAN_ARTIFACT="$TMP_DIR/ledgerbot-guardian"

mkdir -p "$OLD_RELEASE"
printf 'old ledgerbot\n' >"$OLD_RELEASE/ledgerbot"
printf 'old guardian\n' >"$OLD_RELEASE/ledgerbot-guardian"
ln -s "$OLD_RELEASE" "$ROOT/current"
printf 'new ledgerbot\n' >"$LEDGERBOT_ARTIFACT"
printf 'new guardian\n' >"$GUARDIAN_ARTIFACT"

OLD_CURRENT="$(readlink -f "$ROOT/current")"
if bash "$PROJECT_DIR/scripts/deploy_release_layout.sh" \
	--root "$ROOT" \
	--release-id release-new \
	--ledgerbot "$LEDGERBOT_ARTIFACT" \
	--guardian "$GUARDIAN_ARTIFACT" \
	--expected-ledgerbot-sha "$(printf '0%.0s' {1..64})"; then
	echo "checksum mismatch must fail" >&2
	exit 1
fi
[[ "$(readlink -f "$ROOT/current")" == "$OLD_CURRENT" ]]

LEDGERBOT_SHA="$(sha256sum "$LEDGERBOT_ARTIFACT" | awk '{print $1}')"
bash "$PROJECT_DIR/scripts/deploy_release_layout.sh" \
	--root "$ROOT" \
	--release-id release-new \
	--ledgerbot "$LEDGERBOT_ARTIFACT" \
	--guardian "$GUARDIAN_ARTIFACT" \
	--expected-ledgerbot-sha "$LEDGERBOT_SHA"

NEW_RELEASE="$ROOT/releases/release-new"
[[ "$(readlink -f "$ROOT/current")" == "$NEW_RELEASE" ]]
[[ "$(stat -c '%a' "$ROOT/releases")" == "755" ]]
[[ "$(stat -c '%a' "$NEW_RELEASE")" == "755" ]]
[[ -x "$NEW_RELEASE/ledgerbot" ]]
[[ -x "$NEW_RELEASE/ledgerbot-guardian" ]]
grep -F '"release_id":"release-new"' "$NEW_RELEASE/manifest.json" >/dev/null
grep -F '"rollback_eligible":false' "$NEW_RELEASE/manifest.json" >/dev/null
grep -F 'RELEASE_ID=release-new' "$NEW_RELEASE/release.env" >/dev/null
grep -F "RELEASE_SHA256=$LEDGERBOT_SHA" "$NEW_RELEASE/release.env" >/dev/null

if bash "$PROJECT_DIR/scripts/deploy_release_layout.sh" \
	--root "$ROOT" \
	--release-id release-new \
	--ledgerbot "$LEDGERBOT_ARTIFACT" \
	--guardian "$GUARDIAN_ARTIFACT"; then
	echo "existing release identifier must fail" >&2
	exit 1
fi
[[ "$(readlink -f "$ROOT/current")" == "$NEW_RELEASE" ]]

echo "release layout tests passed"
