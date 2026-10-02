#!/usr/bin/env python3
"""One-shot e2e funding transfer from a Macro Wallets base address, through the external API
(POST /api/v1/wallets/{id}/withdrawals) with the Markets API token.

Guards, all before the single POST:
  - the funding ledger has no entry with this tag and vault_test has no outbound transaction
    of this wallet to this address for this amount;
  - nobody holds the custody recording lock (this script holds it while it sends);
  - a claim file <locks>/send-<tag>.claim is created exclusively and never removed, so a
    second run with the same tag refuses until a human reviews it;
  - scripts/e2e/preflight.py plans, MPC-signs and verifies the same transfer off-chain.
The idempotency key is a UUIDv5 of the tag, so even a repeated POST returns the original
withdrawal. Token and passphrase stay in memory and are never printed.

Usage:
  scripts/e2e/send_from_base.py TAG WALLET_ID ASSET AMOUNT_BASE_UNITS DECIMALS TO EXTERNAL_USER_ID [--apply]
Without --apply it only runs the guards and the pre-flight.
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import re
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid
from decimal import Decimal
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from e2e_state import FUNDING_DIR, FUNDING_LEDGER, LOCKS_DIR, RECORDING_LOCK, ensure_private_dir, write_private_file  # noqa: E402
from preflight import ADDRESS_PATTERN, AMOUNT_PATTERN, ASSET_PATTERN, UUID_PATTERN, require, verified_passphrase  # noqa: E402

API_BASE_URL = os.environ.get("MACRO_WALLETS_API_URL", "http://localhost:2002").rstrip("/")
MARKETS_CONTAINER = os.environ.get("MARKETS_CONTAINER", "markets-custody-e2e")
MARKETS_PHP = os.environ.get("MARKETS_PHP", "/usr/bin/php8.5.real")
WALLETS_DB_CONTAINER = os.environ.get("WALLETS_DB_CONTAINER", "waas-postgres")
WALLETS_DB = "vault_test"
IDEMPOTENCY_NAMESPACE = "macro-e2e-funding:"
TAG_PATTERN = re.compile(r"^[a-z0-9][a-z0-9-]{2,80}$")
EXTERNAL_USER_PATTERN = re.compile(r"^[0-9]{1,12}$")
MAX_DECIMALS = 36
HTTP_TIMEOUT_SECONDS = 120
TX_HASH_POLL_SECONDS = 10
TX_HASH_WAIT_SECONDS = 600
TOKEN_PREFIX = "TOKEN="
PREFLIGHT_SCRIPT = Path(__file__).resolve().parent / "preflight.py"


def now_iso() -> str:
    return datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds")


def load_ledger() -> dict:
    if not FUNDING_LEDGER.is_file():
        raise SystemExit(f"missing funding ledger {FUNDING_LEDGER}")
    ledger = json.loads(FUNDING_LEDGER.read_text())
    if not isinstance(ledger.get("entries"), list):
        raise SystemExit(f"{FUNDING_LEDGER} has no entries list")
    return ledger


def upsert_ledger_entry(tag: str, entry: dict) -> None:
    ledger = load_ledger()
    entries = [existing for existing in ledger["entries"] if existing.get("tag") != tag]
    ledger["entries"] = [*entries, entry]
    ledger["recordedAt"] = now_iso()
    write_private_file(FUNDING_LEDGER, (json.dumps(ledger, indent=2) + "\n").encode())


def vault_outbound_matches(wallet_id: str, to: str, base_units: str) -> list[str]:
    sql = (
        "select id || ' ' || status || ' ' || coalesce(tx_hash,'') from transactions "
        f"where wallet_id = '{wallet_id}' and direction = 'outbound' and lower(to_address) = lower('{to}') "
        f"and amount = '{base_units}'"
    )
    completed = subprocess.run(
        ["docker", "exec", WALLETS_DB_CONTAINER, "psql", "-U", "vault", "-d", WALLETS_DB, "-At", "-c", sql],
        capture_output=True,
        text=True,
        timeout=60,
    )
    if completed.returncode != 0:
        raise SystemExit(f"vault_test query failed: {completed.stderr.strip()[-400:]}")
    return [line for line in completed.stdout.splitlines() if line.strip()]


def acquire_recording_lock(owner: str) -> None:
    started = now_iso()
    payload = {"owner": owner, "pid": os.getpid(), "host": socket.gethostname(), "startedAt": started, "heartbeatAt": started}
    try:
        descriptor = os.open(RECORDING_LOCK, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        holder = RECORDING_LOCK.read_text(errors="replace")[:300]
        raise SystemExit(f"recording lock is held, not sending: {holder}")
    with os.fdopen(descriptor, "w") as handle:
        handle.write(json.dumps(payload))


def release_recording_lock(owner: str) -> None:
    try:
        if json.loads(RECORDING_LOCK.read_text()).get("owner") == owner:
            RECORDING_LOCK.unlink()
    except (FileNotFoundError, json.JSONDecodeError):
        pass


def claim_send(tag: str, details: dict) -> Path:
    ensure_private_dir(LOCKS_DIR)
    claim = LOCKS_DIR / f"send-{tag}.claim"
    try:
        descriptor = os.open(claim, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        raise SystemExit(f"{claim} exists: this transfer was already attempted; review it before anything else")
    with os.fdopen(descriptor, "w") as handle:
        handle.write(json.dumps({**details, "claimedAt": now_iso()}, indent=2))
    return claim


def run_preflight(wallet_id: str, asset: str, base_units: str, to: str) -> dict:
    completed = subprocess.run(
        [sys.executable, str(PREFLIGHT_SCRIPT), "withdrawal", wallet_id, asset, base_units, to],
        capture_output=True,
        text=True,
        timeout=600,
    )
    if completed.returncode != 0:
        raise SystemExit(f"pre-flight failed:\n{(completed.stdout + completed.stderr)[-1500:]}")
    result = json.loads(completed.stdout)
    transfers = result.get("transactions") or []
    if len(transfers) != 1 or transfers[0].get("to") != to or transfers[0].get("amount") != base_units:
        raise SystemExit(f"pre-flight planned something else: {json.dumps(transfers)[:600]}")
    return result


def markets_api_token() -> str:
    source = "echo '" + TOKEN_PREFIX + "'.app(App\\Settings\\CryptoCustodySettings::class)->macro_wallets_api_token.PHP_EOL;"
    command = (
        'exec env $(tr "\\0" "\\n" < /proc/1/environ | grep -E "^(DB_|REDIS_|APP_)" | tr "\\n" " ") '
        f"{MARKETS_PHP} artisan tinker --execute=\"$0\""
    )
    completed = subprocess.run(
        ["docker", "exec", "-w", "/var/www/html", MARKETS_CONTAINER, "sh", "-c", command, source],
        capture_output=True,
        text=True,
        timeout=120,
    )
    tokens = [line[len(TOKEN_PREFIX):].strip() for line in completed.stdout.splitlines() if line.startswith(TOKEN_PREFIX)]
    if completed.returncode != 0 or not tokens or not tokens[-1]:
        raise SystemExit("could not read the Markets Macro Wallets API token")
    return tokens[-1]


def api_request(method: str, path: str, token: str, body: dict | None = None) -> tuple[int, dict]:
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(f"{API_BASE_URL}{path}", data=data, method=method)
    request.add_header("Authorization", f"Bearer {token}")
    request.add_header("Accept", "application/json")
    if data is not None:
        request.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(request, timeout=HTTP_TIMEOUT_SECONDS) as response:
            return response.status, json.loads(response.read() or b"{}")
    except urllib.error.HTTPError as error:
        raw = error.read()
        try:
            return error.code, json.loads(raw or b"{}")
        except json.JSONDecodeError:
            return error.code, {"raw": raw.decode(errors="replace")[:500]}


def find_tx_hash(payload: dict) -> str:
    for container in (payload, payload.get("data") or {}, payload.get("withdrawal") or {}):
        if isinstance(container, dict) and container.get("tx_hash"):
            return str(container["tx_hash"])
    return ""


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("tag")
    parser.add_argument("wallet_id")
    parser.add_argument("asset")
    parser.add_argument("amount")
    parser.add_argument("decimals", type=int)
    parser.add_argument("to")
    parser.add_argument("external_user_id")
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()

    tag = require(TAG_PATTERN, args.tag, "tag")
    wallet_id = require(UUID_PATTERN, args.wallet_id, "wallet id")
    asset = require(ASSET_PATTERN, args.asset, "asset")
    base_units = require(AMOUNT_PATTERN, args.amount, "amount (base units)")
    to = require(ADDRESS_PATTERN, args.to, "destination address")
    external_user_id = require(EXTERNAL_USER_PATTERN, args.external_user_id, "external user id")
    if not 0 <= args.decimals <= MAX_DECIMALS:
        raise SystemExit(f"invalid decimals {args.decimals}")
    human_amount = format(Decimal(base_units).scaleb(-args.decimals), "f")
    idempotency_key = str(uuid.uuid5(uuid.NAMESPACE_URL, IDEMPOTENCY_NAMESPACE + tag))

    if any(entry.get("tag") == tag for entry in load_ledger()["entries"]):
        raise SystemExit(f"funding ledger already has {tag}; not sending again")
    existing = vault_outbound_matches(wallet_id, to, base_units)
    if existing:
        raise SystemExit(f"vault_test already has this outbound transfer: {existing}")

    preflight = run_preflight(wallet_id, asset, base_units, to)
    plan = {"tag": tag, "wallet_id": wallet_id, "asset": asset, "amount": human_amount, "base_units": base_units,
            "to": to, "from": preflight["transactions"][0].get("from"), "idempotency_key": idempotency_key}
    print("plan " + json.dumps(plan))
    if not args.apply:
        print("dry run: pre-flight verified, nothing sent (add --apply to send once)")
        return 0

    owner = f"send-from-base:{tag}"
    acquire_recording_lock(owner)
    try:
        claim = claim_send(tag, plan)
        token = markets_api_token()
        passphrase = verified_passphrase(wallet_id)
        status, response = api_request("POST", f"/api/v1/wallets/{wallet_id}/withdrawals", token, {
            "external_user_id": external_user_id,
            "destination_address": to,
            "amount": human_amount,
            "asset": asset,
            "passphrase": passphrase,
            "idempotency_key": idempotency_key,
        })
        del passphrase
        entry = {"chain": asset.lower(), "tag": tag, "purpose": "e2e funding transfer from base via Wallets API",
                 "source": plan["from"], "destination": to, "amount": f"{base_units} base units ({human_amount} {asset})",
                 "idempotency_key": idempotency_key, "http_status": status, "hash": find_tx_hash(response),
                 "status": "submitted", "recordedAt": now_iso()}
        upsert_ledger_entry(tag, entry)
        write_private_file(FUNDING_DIR / f"{tag}.json", (json.dumps({"plan": plan, "status": status, "response": response}, indent=2) + "\n").encode())
        print(f"POST status {status}; claim {claim}")
        if status >= 300:
            print(json.dumps(response)[:800], file=sys.stderr)
            return 1

        deadline = time.monotonic() + TX_HASH_WAIT_SECONDS
        while not entry["hash"] and time.monotonic() < deadline:
            time.sleep(TX_HASH_POLL_SECONDS)
            _, lookup = api_request("GET", f"/api/v1/wallets/{wallet_id}/withdrawals/{idempotency_key}", token)
            entry["hash"] = find_tx_hash(lookup)
            entry["lookup_status"] = (lookup.get("data") or lookup).get("status") if isinstance(lookup, dict) else None
        entry["status"] = "broadcast" if entry["hash"] else "submitted, no tx hash yet"
        entry["recordedAt"] = now_iso()
        upsert_ledger_entry(tag, entry)
        print(f"tx hash: {entry['hash'] or '(none yet)'}")
        return 0 if entry["hash"] else 2
    finally:
        release_recording_lock(owner)


if __name__ == "__main__":
    sys.exit(main())
