#!/usr/bin/env bash
# Deploy the API to a Forge host that runs it as a daemon (the dev and production
# layout of docs/AWS_DEV_STACK.md): build the linux/arm64 binary here, rsync it as a
# new release, point `current` at it, run the migrations and restart the daemons
# through the Forge API. Nothing is compiled on the host (2 GB of RAM) and no sudo is
# needed. The host's `.env` is the file at <site>/.env, linked into each release.
#
#   scripts/deploy-host.sh ENV [REV]
#
# ENV names ~/.config/macro-wallets/deploy-<ENV>.conf, a dotenv with:
#   SERVER_SSH=forge@<host>            FORGE_ORG=cods
#   SITE_DIR=/home/forge/<site>        FORGE_SERVER=<server id>
#   DAEMON_IDS=<api daemon id>[,<poller daemon id>]
#   FORGE_TOKEN_FILE=~/.config/macro-wallets/forge-token
#   KEEP_RELEASES=3
# REV is a git revision (default HEAD). The release is a `git archive` of it, so
# uncommitted changes never reach the host.
set -euo pipefail

env_name="${1:?usage: scripts/deploy-host.sh ENV [REV]}"
rev="${2:-HEAD}"
conf="$HOME/.config/macro-wallets/deploy-${env_name}.conf"
[ -f "$conf" ] || { echo "missing $conf" >&2; exit 1; }
# shellcheck disable=SC1090
set -a; . "$conf"; set +a
: "${SERVER_SSH:?}" "${SITE_DIR:?}" "${FORGE_ORG:?}" "${FORGE_SERVER:?}" "${DAEMON_IDS:?}"
FORGE_TOKEN_FILE="${FORGE_TOKEN_FILE:-$HOME/.config/macro-wallets/forge-token}"
KEEP_RELEASES="${KEEP_RELEASES:-3}"
SSH_KEY="${SSH_KEY:-}"
ssh_opts=(-o BatchMode=yes -o ConnectTimeout=15)
[ -n "$SSH_KEY" ] && ssh_opts+=(-i "$SSH_KEY")

repo="$(git rev-parse --show-toplevel)"
sha="$(git -C "$repo" rev-parse --short=12 "$rev")"
stamp="$(date -u +%Y%m%d%H%M%S)"
release="$SITE_DIR/releases/$stamp"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "==> source $rev ($sha)"
git -C "$repo" archive --format=tar "$rev" | tar -x -C "$work"

echo "==> build linux/arm64"
( cd "$work" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags "-s -w" -o wallet-service . )
printf '%s\n' "$sha" > "$work/REVISION"

echo "==> upload to $release"
ssh "${ssh_opts[@]}" "$SERVER_SSH" "mkdir -p '$release'"
rsync -az -e "ssh ${ssh_opts[*]}" \
    "$work/wallet-service" "$work/REVISION" "$work/database" "$work/docs" \
    "$SERVER_SSH:$release/"

echo "==> link .env, migrate, activate"
ssh "${ssh_opts[@]}" "$SERVER_SSH" bash -s -- "$SITE_DIR" "$release" "$KEEP_RELEASES" <<'EOF'
set -euo pipefail
site="$1"; release="$2"; keep="$3"
[ -f "$site/.env" ] || { echo "no $site/.env on the host" >&2; exit 1; }
ln -sfn "$site/.env" "$release/.env"
( cd "$release" && ./wallet-service artisan migrate )
ln -sfn "$release" "$site/current.tmp" && mv -Tf "$site/current.tmp" "$site/current"
echo "current -> $(readlink "$site/current")"
# prune: keep the newest $keep releases
ls -1d "$site"/releases/* | sort | head -n -"$keep" | xargs -r rm -rf
EOF

echo "==> restart daemons ${DAEMON_IDS}"
token="$(tr -d '[:space:]' < "$FORGE_TOKEN_FILE")"
IFS=',' read -ra ids <<< "$DAEMON_IDS"
for id in "${ids[@]}"; do
    code="$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        -H "Authorization: Bearer $token" -H "Accept: application/vnd.api+json" \
        -H "Content-Type: application/json" -d '{"action":"restart"}' \
        "https://forge.laravel.com/api/orgs/$FORGE_ORG/servers/$FORGE_SERVER/background-processes/$id/actions")"
    echo "daemon $id restart -> HTTP $code"
    case "$code" in 2*) ;; *) echo "restart of daemon $id failed" >&2; exit 1;; esac
done
echo "==> deployed $sha"
