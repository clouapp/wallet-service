#!/usr/bin/env python3
"""Off-chain pre-flight of a withdrawal or a consolidation: plan, MPC-sign, verify the
signature against the source address. Nothing is broadcast or persisted.

The wallet passphrase comes from the encrypted wallet vault (wallet-vault.py backup, only a
passphrase verified against share A); it stays in memory and goes to
`<state dir>/bin/waas-e2e-bin artisan withdraw:preflight` on stdin.

Usage:
  scripts/e2e/preflight.py withdrawal WALLET_ID ASSET AMOUNT_BASE_UNITS TO_ADDRESS
  scripts/e2e/preflight.py consolidation WALLET_ID ASSET
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from types import ModuleType

sys.path.insert(0, str(Path(__file__).resolve().parent))

from e2e_state import API_BINARY, WALLETS_BACK_DIR, read_api_environ  # noqa: E402

WALLET_VAULT_SCRIPT = Path(
    os.environ.get("MACRO_E2E_WALLET_VAULT", "/home/raphaelcangucu/macro-markets/back/tests/e2e/wallet-vault.py")
)
UUID_PATTERN = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
ASSET_PATTERN = re.compile(r"^[A-Z0-9]{2,12}$")
AMOUNT_PATTERN = re.compile(r"^[1-9][0-9]{0,40}$")
ADDRESS_PATTERN = re.compile(r"^[A-Za-z0-9]{20,100}$")
PREFLIGHT_TIMEOUT_SECONDS = 300
RESULT_PREFIX = "PREFLIGHT "


def load_wallet_vault() -> ModuleType:
    spec = importlib.util.spec_from_file_location("wallet_vault", WALLET_VAULT_SCRIPT)
    if spec is None or spec.loader is None:
        raise SystemExit(f"cannot load {WALLET_VAULT_SCRIPT}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def verified_passphrase(wallet_id: str) -> str:
    vault = load_wallet_vault()
    record = vault.load_record(vault.wallet_path(wallet_id))
    if not record.get("passphrase") or not record.get("passphrase_verified"):
        raise SystemExit(f"no verified passphrase for {wallet_id} in the wallet vault; run wallet-vault.py backup")
    return record["passphrase"]


def require(pattern: re.Pattern[str], value: str, what: str) -> str:
    if not pattern.match(value):
        raise SystemExit(f"invalid {what}: {value!r}")
    return value


def build_request(args: argparse.Namespace) -> dict[str, str]:
    request = {
        "mode": args.mode,
        "wallet_id": require(UUID_PATTERN, args.wallet_id, "wallet id"),
        "asset": require(ASSET_PATTERN, args.asset, "asset"),
    }
    if args.mode == "withdrawal":
        request["amount"] = require(AMOUNT_PATTERN, args.amount, "amount (base units)")
        request["to"] = require(ADDRESS_PATTERN, args.to, "destination address")
    return request


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    modes = parser.add_subparsers(dest="mode", required=True)
    withdrawal = modes.add_parser("withdrawal")
    withdrawal.add_argument("wallet_id")
    withdrawal.add_argument("asset")
    withdrawal.add_argument("amount")
    withdrawal.add_argument("to")
    consolidation = modes.add_parser("consolidation")
    consolidation.add_argument("wallet_id")
    consolidation.add_argument("asset")
    args = parser.parse_args()

    request = build_request(args)
    request["passphrase"] = verified_passphrase(request["wallet_id"])
    if not API_BINARY.is_file():
        raise SystemExit(f"{API_BINARY} is missing; run scripts/e2e/start_wallets_api.py --build")

    completed = subprocess.run(
        [str(API_BINARY), "artisan", "withdraw:preflight"],
        cwd=WALLETS_BACK_DIR,
        env=read_api_environ(),
        input=json.dumps(request).encode(),
        capture_output=True,
        timeout=PREFLIGHT_TIMEOUT_SECONDS,
    )
    request.pop("passphrase")
    result_lines = [line for line in completed.stdout.decode(errors="replace").splitlines() if line.startswith(RESULT_PREFIX)]
    if completed.returncode != 0 or not result_lines:
        tail = (completed.stdout + completed.stderr).decode(errors="replace")[-1500:]
        print(f"pre-flight failed (exit {completed.returncode}):\n{tail}", file=sys.stderr)
        return 1
    result = json.loads(result_lines[-1][len(RESULT_PREFIX):])
    print(json.dumps(result, indent=2))
    if not result.get("signature_verified") or result.get("broadcast") is not False:
        print("pre-flight did not verify the signature or reported a broadcast", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
