#!/usr/bin/env python3
"""One-shot consolidation (child-address sweep into the base) through the external API
(POST /api/v1/wallets/{id}/consolidate), with the same guards as send_from_base.py:
recording lock held while sending, an exclusive never-removed claim per tag, a fresh
off-chain pre-flight of the same sweep, a UUIDv5 idempotency key, and a ledger entry.

Usage:
  scripts/e2e/consolidate_once.py TAG WALLET_ID ASSET [--apply]
Without --apply it only runs the guards and the pre-flight.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from e2e_state import FUNDING_DIR, write_private_file  # noqa: E402
from preflight import ASSET_PATTERN, UUID_PATTERN, require, verified_passphrase  # noqa: E402
from send_from_base import (  # noqa: E402
    IDEMPOTENCY_NAMESPACE,
    PREFLIGHT_SCRIPT,
    TAG_PATTERN,
    acquire_recording_lock,
    api_request,
    claim_send,
    load_ledger,
    markets_api_token,
    now_iso,
    release_recording_lock,
    upsert_ledger_entry,
)

PREFLIGHT_TIMEOUT_SECONDS = 600


def run_consolidation_preflight(wallet_id: str, asset: str) -> dict:
    completed = subprocess.run(
        [sys.executable, str(PREFLIGHT_SCRIPT), "consolidation", wallet_id, asset],
        capture_output=True,
        text=True,
        timeout=PREFLIGHT_TIMEOUT_SECONDS,
    )
    if completed.returncode != 0:
        raise SystemExit(f"pre-flight failed:\n{(completed.stdout + completed.stderr)[-1500:]}")
    result = json.loads(completed.stdout)
    if not result.get("transactions"):
        raise SystemExit("pre-flight planned no sweep; nothing to consolidate")
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("tag")
    parser.add_argument("wallet_id")
    parser.add_argument("asset")
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()

    tag = require(TAG_PATTERN, args.tag, "tag")
    wallet_id = require(UUID_PATTERN, args.wallet_id, "wallet id")
    asset = require(ASSET_PATTERN, args.asset, "asset")
    idempotency_key = str(uuid.uuid5(uuid.NAMESPACE_URL, IDEMPOTENCY_NAMESPACE + tag))
    if any(entry.get("tag") == tag for entry in load_ledger()["entries"]):
        raise SystemExit(f"funding ledger already has {tag}; not consolidating again")

    preflight = run_consolidation_preflight(wallet_id, asset)
    plan = {"tag": tag, "wallet_id": wallet_id, "asset": asset, "strategy": preflight.get("strategy"),
            "sweeps": [{key: sweep.get(key) for key in ("from", "to", "amount")} for sweep in preflight["transactions"]],
            "idempotency_key": idempotency_key}
    print("plan " + json.dumps(plan))
    if not args.apply:
        print("dry run: pre-flight verified, nothing sent (add --apply to consolidate once)")
        return 0

    owner = f"consolidate-once:{tag}"
    acquire_recording_lock(owner)
    try:
        claim = claim_send(tag, plan)
        token = markets_api_token()
        passphrase = verified_passphrase(wallet_id)
        status, response = api_request("POST", f"/api/v1/wallets/{wallet_id}/consolidate", token, {
            "asset": asset,
            "passphrase": passphrase,
            "idempotency_key": idempotency_key,
        })
        del passphrase
        write_private_file(FUNDING_DIR / f"{tag}.json",
                           (json.dumps({"plan": plan, "status": status, "response": response}, indent=2) + "\n").encode())
        upsert_ledger_entry(tag, {"chain": asset.lower(), "tag": tag, "purpose": "child-address sweep into the base via Wallets API",
                                  "sweeps": plan["sweeps"], "idempotency_key": idempotency_key, "http_status": status,
                                  "response_file": str(FUNDING_DIR / f"{tag}.json"), "status": "submitted", "recordedAt": now_iso()})
        print(f"POST status {status}; claim {claim}")
        print(json.dumps(response, indent=1)[:2000])
        return 0 if status < 300 else 1
    finally:
        release_recording_lock(owner)


if __name__ == "__main__":
    sys.exit(main())
