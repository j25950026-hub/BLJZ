#!/usr/bin/env bash
set -Eeuo pipefail

BASE="/opt/bilibili-ledgerbot-20260704"
STAGE="$BASE/.v2-cutover-20260803-0100"
RELEASE="$STAGE/release"
SERVICE_NAME="bilibili-ledgerbot.service"
STAMP="$(date '+%Y%m%d_%H%M%S')"
BACKUP="$BASE/rollback/v2-cutover-$STAMP"
LOG="$BASE/logs/v2_cutover_$STAMP.log"
CUTOVER_STARTED=0
BACKUP_WEB_EXISTS=0
SETUP_TOKEN_FILE="$STAGE/.admin_setup_token"

umask 077
mkdir -p "$BACKUP" "$BASE/logs"
exec > >(tee -a "$LOG") 2>&1

rollback() {
    set +e
    CUTOVER_STARTED=0
    echo "[rollback] restoring previous LedgerBot release"
    systemctl stop "$SERVICE_NAME" || true

    if [[ -f "$BACKUP/ledgerbot" ]]; then
        install -o root -g root -m 0755 "$BACKUP/ledgerbot" "$BASE/ledgerbot"
    fi
    if [[ -f "$BACKUP/.env" ]]; then
        install -o root -g root -m 0600 "$BACKUP/.env" "$BASE/.env"
    fi
    if [[ -f "$BACKUP/bilibili-ledgerbot.service" ]]; then
        install -o root -g root -m 0644 "$BACKUP/bilibili-ledgerbot.service" "/etc/systemd/system/$SERVICE_NAME"
    fi
    if [[ -d "$BACKUP/web-admin-app" ]]; then
        rm -rf "$BASE/web/admin-app"
        install -d -o root -g root -m 0755 "$BASE/web/admin-app"
        cp -a "$BACKUP/web-admin-app/." "$BASE/web/admin-app/"
    elif [[ "$BACKUP_WEB_EXISTS" == "0" ]]; then
        rm -rf "$BASE/web/admin-app"
    fi

    systemctl daemon-reload || true
    systemctl start "$SERVICE_NAME" || true
    echo "[rollback] completed; inspect $LOG"
}

on_exit() {
    rc=$?
    if [[ "$rc" -ne 0 && "$CUTOVER_STARTED" -eq 1 ]]; then
        rollback
    fi
    rm -f "$SETUP_TOKEN_FILE" "$STAGE/.env.without-setup-token" "$STAGE/.env.next" "$STAGE/.env.before-cutover"
    exit "$rc"
}
trap on_exit EXIT

if [[ "$(id -u)" -ne 0 ]]; then
    echo "cutover must run as root" >&2
    exit 1
fi

for required in \
    "$RELEASE/ledgerbot" \
    "$RELEASE/web/admin-app/index.html" \
    "$RELEASE/web/admin-app/app.js" \
    "$RELEASE/web/admin-app/styles.css" \
    "$RELEASE/SHA256SUMS" \
    "$STAGE/bilibili-ledgerbot-v2.service"; do
    test -s "$required"
done
test -s "$SETUP_TOKEN_FILE"

(
    cd "$RELEASE"
    sha256sum -c SHA256SUMS
)

systemctl is-active --quiet "$SERVICE_NAME"
test -f "$BASE/.env"
test -f "$BASE/ledgerbot"

cp -a "$BASE/ledgerbot" "$BACKUP/ledgerbot"
cp -a "$BASE/.env" "$BACKUP/.env"
cp -a "/etc/systemd/system/$SERVICE_NAME" "$BACKUP/bilibili-ledgerbot.service"
if [[ -d "$BASE/web/admin-app" ]]; then
    BACKUP_WEB_EXISTS=1
    mkdir -p "$BACKUP/web-admin-app"
    cp -a "$BASE/web/admin-app/." "$BACKUP/web-admin-app/"
else
    BACKUP_WEB_EXISTS=0
fi

echo "[cutover] creating runtime user if needed"
if ! id ledgerbot >/dev/null 2>&1; then
    useradd --system --user-group --home-dir "$BASE" --shell /usr/sbin/nologin --no-create-home ledgerbot
fi
getent group ledgerbot >/dev/null 2>&1

CUTOVER_STARTED=1
systemctl stop "$SERVICE_NAME"

install -d -o ledgerbot -g ledgerbot -m 0750 "$BASE/data" "$BASE/logs"
find "$BASE/data" -type d -exec chown ledgerbot:ledgerbot {} +
find "$BASE/data" -type d -exec chmod 0750 {} +
find "$BASE/data" -type f -exec chown ledgerbot:ledgerbot {} +
find "$BASE/data" -type f -exec chmod 0600 {} +
find "$BASE/logs" -type d -exec chown ledgerbot:ledgerbot {} +
find "$BASE/logs" -type d -exec chmod 0750 {} +
find "$BASE/logs" -type f -exec chown ledgerbot:ledgerbot {} +
find "$BASE/logs" -type f -exec chmod 0600 {} +
if [[ -f "$BASE/data/bot_go.db" ]]; then
    chown ledgerbot:ledgerbot "$BASE/data/bot_go.db"
    chmod 0600 "$BASE/data/bot_go.db"
fi

install -o root -g root -m 0755 "$RELEASE/ledgerbot" "$BASE/ledgerbot"
rm -rf "$BASE/web/admin-app"
install -d -o root -g root -m 0755 "$BASE/web/admin-app"
install -o root -g root -m 0644 "$RELEASE/web/admin-app/index.html" "$BASE/web/admin-app/index.html"
install -o root -g root -m 0644 "$RELEASE/web/admin-app/app.js" "$BASE/web/admin-app/app.js"
install -o root -g root -m 0644 "$RELEASE/web/admin-app/styles.css" "$BASE/web/admin-app/styles.css"

cp -a "$BASE/.env" "$STAGE/.env.before-cutover"
awk '
    BEGIN { replaced = 0 }
    /^K8S_PROBE_BIND_ADDR=/ { print "K8S_PROBE_BIND_ADDR=127.0.0.1:18080"; replaced = 1; next }
    { print }
    END { if (!replaced) print "K8S_PROBE_BIND_ADDR=127.0.0.1:18080" }
' "$BASE/.env" > "$STAGE/.env.next"

setup_token_line="$(tr -d '\r\n' < "$SETUP_TOKEN_FILE")"
if [[ "$setup_token_line" == ADMIN_SETUP_TOKEN=* ]]; then
    setup_token="${setup_token_line#ADMIN_SETUP_TOKEN=}"
else
    setup_token="$setup_token_line"
fi
if [[ ! "$setup_token" =~ ^[A-Fa-f0-9]{64}$ ]]; then
    echo "ADMIN_SETUP_TOKEN must be a 256-bit hexadecimal value" >&2
    exit 1
fi
grep -v '^ADMIN_SETUP_TOKEN=' "$STAGE/.env.next" > "$STAGE/.env.without-setup-token"
printf 'ADMIN_SETUP_TOKEN=%s\n' "$setup_token" >> "$STAGE/.env.without-setup-token"
install -o root -g root -m 0600 "$STAGE/.env.without-setup-token" "$STAGE/.env.next"
install -o ledgerbot -g ledgerbot -m 0600 "$STAGE/.env.next" "$BASE/.env"

runuser -u ledgerbot -- test -r "$BASE/ledgerbot"
runuser -u ledgerbot -- test -r "$BASE/.env"
runuser -u ledgerbot -- test -w "$BASE/data"
runuser -u ledgerbot -- test -w "$BASE/logs"

install -o root -g root -m 0644 "$STAGE/bilibili-ledgerbot-v2.service" "/etc/systemd/system/$SERVICE_NAME"
systemctl daemon-reload
systemctl start "$SERVICE_NAME"
sleep 5
systemctl is-active --quiet "$SERVICE_NAME"
[[ "$(systemctl show -p User --value "$SERVICE_NAME")" == "ledgerbot" ]]
curl -fsS --max-time 10 http://127.0.0.1:18080/healthz >/dev/null
curl -fsS --max-time 10 http://127.0.0.1:18080/readyz >/dev/null
ss -ltn | grep -Eq '127\.0\.0\.1:18080([[:space:]]|$)'
if ss -ltn | grep -Eq '(0\.0\.0\.0|\[::\]|\*:)[0-9]*18080'; then
    echo "health probe is still exposed on a wildcard address" >&2
    exit 1
fi
test -s "$BASE/web/admin-app/index.html"
test -s "$BASE/web/admin-app/app.js"
test -s "$BASE/web/admin-app/styles.css"
rm -f "$SETUP_TOKEN_FILE" "$STAGE/.env.without-setup-token" "$STAGE/.env.next" "$STAGE/.env.before-cutover"

echo "[cutover] V2 release is active as user=ledgerbot"
echo "[cutover] health probe is bound to 127.0.0.1:18080"
echo "[cutover] admin=https://autobookbl.site/admin"
echo "[cutover] bill=https://autobookbl.site/tg/webapp/looklook.php"
CUTOVER_STARTED=0
