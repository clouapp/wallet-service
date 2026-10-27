#!/usr/bin/env bash
# Deploy the API to a Forge host that runs it as a daemon (the dev and production
# layout of docs/AWS_DEV_STACK.md): build the binary here (linux, GOARCH of the host),
# rsync it as a new release, run the migrations, point `current` at it, restart the
# daemons through the Forge API and wait for /health; a release whose health check
# fails is rolled back to the previous one. Nothing is compiled on the host and no
# sudo is needed. The host's `.env` is the file at <site>/.env, linked into each release.
#
#   scripts/deploy-host.sh ENV [REV]
#
# The settings come from the environment (the CD workflow) or, for a run from a
# workstation, from ~/.config/macro-wallets/deploy-<ENV>.conf (a dotenv):
#   SERVER_SSH=forge@<host>            FORGE_ORG=cods
#   SITE_DIR=/home/forge/<site>        FORGE_SERVER=<server id>
#   DAEMON_IDS=<api daemon id>[,<poller daemon id>]
#   GOARCH=arm64|amd64                 (the host's architecture, default arm64)
#   HEALTH_URL=http://127.0.0.1:2002/health   (read on the host)
#   FORGE_TOKEN=<token> or FORGE_TOKEN_FILE=~/.config/macro-wallets/forge-token
#   SSH_KEY=<private key file>         (optional; the agent or ~/.ssh/config otherwise)
#   KEEP_RELEASES=3
# REV is a git revision (default HEAD). The release is a `git archive` of it, so
# uncommitted changes never reach the host.
set -euo pipefail

env_name="${1:?usage: scripts/deploy-host.sh ENV [REV]}"
rev="${2:-HEAD}"
conf="$HOME/.config/macro-wallets/deploy-${env_name}.conf"
if [ -f "$conf" ]; then
    # shellcheck disable=SC1090
    set -a; . "$conf"; set +a
fi
: "${SERVER_SSH:?}" "${SITE_DIR:?}" "${FORGE_ORG:?}" "${FORGE_SERVER:?}" "${DAEMON_IDS:?}"
GOARCH="${GOARCH:-arm64}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:2002/health}"
KEEP_RELEASES="${KEEP_RELEASES:-3}"
SSH_KEY="${SSH_KEY:-}"
if [ -z "${FORGE_TOKEN:-}" ]; then
    FORGE_TOKEN="$(tr -d '[:space:]' < "${FORGE_TOKEN_FILE:-$HOME/.config/macro-wallets/forge-token}")"
fi
ssh_opts=(-o BatchMode=yes -o ConnectTimeout=15)
[ -n "$SSH_KEY" ] && ssh_opts+=(-i "$SSH_KEY")

repo="$(git rev-parse --show-toplevel)"
sha="$(git -C "$repo" rev-parse --short=12 "$rev")"
stamp="$(date -u +%Y%m%d%H%M%S)"
release="$SITE_DIR/releases/$stamp-$sha"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "==> source $rev ($sha)"
git -C "$repo" archive --format=tar "$rev" | tar -x -C "$work"

echo "==> build linux/$GOARCH"
( cd "$work" && CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" \
    go build -trimpath -ldflags "-s -w" -o wallet-service . )
printf '%s\n' "$sha" > "$work/REVISION"

echo "==> upload to $release"
ssh "${ssh_opts[@]}" "$SERVER_SSH" "mkdir -p '$release'"
rsync -az -e "ssh ${ssh_opts[*]}" \
    "$work/wallet-service" "$work/REVISION" "$work/database" "$work/docs" \
    "$SERVER_SSH:$release/"

restart_daemons() {
    IFS=',' read -ra ids <<< "$DAEMON_IDS"
    for id in "${ids[@]}"; do
        code="$(curl -s -o /dev/null -w '%{http_code}' -X POST --retry 3 --retry-all-errors \
            -H "Authorization: Bearer $FORGE_TOKEN" -H "Accept: application/vnd.api+json" \
            -H "Content-Type: application/json" -d '{"action":"restart"}' \
            "https://forge.laravel.com/api/orgs/$FORGE_ORG/servers/$FORGE_SERVER/background-processes/$id/actions")"
        echo "daemon $id restart -> HTTP $code"
        case "$code" in 2*) ;; *) echo "restart of daemon $id failed" >&2; return 1;; esac
    done
}

echo "==> link .env, migrate, activate"
previous="$(ssh "${ssh_opts[@]}" "$SERVER_SSH" bash -s -- "$SITE_DIR" "$release" <<'EOF'
set -euo pipefail
site="$1"; release="$2"
[ -f "$site/.env" ] || { echo "no $site/.env on the host" >&2; exit 1; }
ln -sfn "$site/.env" "$release/.env"
( cd "$release" && set -a && . ./.env && set +a && ./wallet-service artisan migrate >&2 )
previous="$(readlink "$site/current" || true)"
ln -sfn "$release" "$site/current.tmp" && mv -Tf "$site/current.tmp" "$site/current"
echo "current -> $(readlink "$site/current")" >&2
echo "$previous"
EOF
)"

echo "==> restart daemons ${DAEMON_IDS}"
restart_daemons

echo "==> health $HEALTH_URL"
if ! ssh "${ssh_opts[@]}" "$SERVER_SSH" "for i in \$(seq 1 30); do curl -fsS -o /dev/null '$HEALTH_URL' && exit 0; sleep 2; done; exit 1"; then
    echo "health check failed; rolling back to ${previous:-<none>}" >&2
    if [ -n "$previous" ]; then
        ssh "${ssh_opts[@]}" "$SERVER_SSH" "ln -sfn '$previous' '$SITE_DIR/current.tmp' && mv -Tf '$SITE_DIR/current.tmp' '$SITE_DIR/current'"
        restart_daemons || true
    fi
    exit 1
fi

echo "==> prune: keep the newest $KEEP_RELEASES releases and the active one"
ssh "${ssh_opts[@]}" "$SERVER_SSH" bash -s -- "$SITE_DIR" "$KEEP_RELEASES" <<'EOF'
set -euo pipefail
site="$1"; keep="$2"
active="$(readlink -f "$site/current")"
# Newest first by modification time: release names from other tools sort differently.
ls -1dt "$site"/releases/*/ | tail -n +"$((keep + 1))" | while read -r dir; do
    dir="${dir%/}"
    [ "$(readlink -f "$dir")" = "$active" ] && continue
    rm -rf "$dir"
done
EOF
echo "==> deployed $sha"
