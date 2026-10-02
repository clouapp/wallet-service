#!/usr/bin/env python3
"""Builds <state dir>/wallets-api.environ (0600) for the e2e Wallets API.

Source: back/.env.dev, plus the e2e overrides below (vault_test, testnet chains, the deposit
scanners). The previous file is kept as wallets-api.environ.prev-<UTC timestamp>. Only key
names are printed, never values.

Usage: scripts/e2e/capture_api_env.py [--dry-run]
"""

from __future__ import annotations

import argparse
import datetime as dt
import os
import re
import shutil
import sys
from pathlib import Path
from urllib.parse import urlparse

sys.path.insert(0, str(Path(__file__).resolve().parent))

from e2e_state import API_ENVIRON, WALLETS_BACK_DIR, write_private_file  # noqa: E402

SOURCE_ENV_FILE = WALLETS_BACK_DIR / ".env.dev"
E2E_DATABASE = "vault_test"
E2E_OVERRIDES: dict[str, str] = {
    "DB_DATABASE": E2E_DATABASE,
    "CHAIN_NETWORK_PROFILE": "testnet",
    "LOCAL_DEPOSIT_SCAN_CHAINS": "sol,eth,btc",
    "BTC_RPC_URL": "https://mempool.space/testnet4/api",
}
REFUSED_DATABASES = {"vault", "vault_unit_test"}
PROCESS_PASSTHROUGH = ("HOME", "PATH", "USER", "LANG", "TZ")
NOT_FOR_THE_API = ("E2E_FUNDER_PRIVATE_KEY",)
SEPOLIA_RPC_KEYS = ("ETH_RPC_URL", "TETH_RPC_URL")
ALCHEMY_SOLANA_DEVNET_HOST = "solana-devnet.g.alchemy.com"
ALCHEMY_SEPOLIA_HOST = "eth-sepolia.g.alchemy.com"
ALCHEMY_KEY_PATH_PREFIX = "/v2/"
ENV_LINE = re.compile(r"^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$")
DATABASE_URL_PATH = re.compile(r"^(postgres(?:ql)?://[^/]+/)([^?]+)(.*)$")


def parse_env_file(path: Path) -> dict[str, str]:
    if not path.is_file():
        raise SystemExit(f"{path} is missing")
    values: dict[str, str] = {}
    for line in path.read_text().splitlines():
        match = ENV_LINE.match(line)
        if not match or line.lstrip().startswith("#"):
            continue
        key, raw = match.group(1), match.group(2).strip()
        if len(raw) >= 2 and raw[0] == raw[-1] and raw[0] in ("'", '"'):
            raw = raw[1:-1]
        values[key] = raw
    return values


def with_e2e_database(database_url: str) -> str:
    match = DATABASE_URL_PATH.match(database_url)
    if not match:
        raise SystemExit("DATABASE_URL in .env.dev is not a postgres URL with a database path")
    return f"{match.group(1)}{E2E_DATABASE}{match.group(3)}"


def alchemy_sepolia_url(solana_rpc_url: str) -> str:
    """Alchemy Sepolia endpoint with the key of the Alchemy Solana devnet endpoint."""
    parsed = urlparse(solana_rpc_url)
    if parsed.hostname != ALCHEMY_SOLANA_DEVNET_HOST or not parsed.path.startswith(ALCHEMY_KEY_PATH_PREFIX):
        raise SystemExit("SOLANA_RPC_URL is not an Alchemy devnet URL; cannot derive the Sepolia RPC")
    return f"https://{ALCHEMY_SEPOLIA_HOST}{parsed.path}"


def build_environment() -> dict[str, str]:
    environment = {key: os.environ[key] for key in PROCESS_PASSTHROUGH if os.environ.get(key)}
    environment.update(parse_env_file(SOURCE_ENV_FILE))
    for key in NOT_FOR_THE_API:
        environment.pop(key, None)
    environment.update(E2E_OVERRIDES)
    sepolia_rpc_url = alchemy_sepolia_url(environment.get("SOLANA_RPC_URL", ""))
    for key in SEPOLIA_RPC_KEYS:
        environment[key] = sepolia_rpc_url
    if "DATABASE_URL" in environment:
        environment["DATABASE_URL"] = with_e2e_database(environment["DATABASE_URL"])
    if environment["DB_DATABASE"] in REFUSED_DATABASES:
        raise SystemExit(f"refusing to run the e2e API on {environment['DB_DATABASE']}")
    for required in ("APP_KEY", "PORT", "DB_PORT", "REDIS_PORT", "AWS_ENDPOINT_URL", "ETH_RPC_URL", "SOLANA_RPC_URL"):
        if not environment.get(required):
            raise SystemExit(f"{required} is empty after merging .env.dev and the e2e overrides")
    return environment


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--dry-run", action="store_true", help="print the key names only; write nothing")
    args = parser.parse_args()

    environment = build_environment()
    print(f"{len(environment)} keys: {' '.join(sorted(environment))}")
    print(f"overrides: {' '.join(f'{key}={value}' for key, value in E2E_OVERRIDES.items())}")
    print(f"{' '.join(SEPOLIA_RPC_KEYS)}: Alchemy Sepolia, key of SOLANA_RPC_URL (not printed)")
    if args.dry_run:
        return 0

    if API_ENVIRON.exists():
        stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        backup = API_ENVIRON.with_name(f"{API_ENVIRON.name}.prev-{stamp}")
        shutil.copy2(API_ENVIRON, backup)
        backup.chmod(0o600)
        print(f"previous environ kept as {backup}")
    payload = b"\0".join(f"{key}={value}".encode() for key, value in sorted(environment.items())) + b"\0"
    write_private_file(API_ENVIRON, payload)
    print(f"wrote {API_ENVIRON}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
