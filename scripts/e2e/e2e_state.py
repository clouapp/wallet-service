"""Paths of the persistent e2e runtime state (outside /tmp, survives reboots).

MACRO_E2E_STATE_DIR overrides the default ~/.local/state/macro-e2e. The same directory is
used by the Markets e2e (macro-markets/front/e2e/helpers/e2eState.ts, wallet-vault.py) and
the Wallets front e2e. Only state and secrets live there; scripts stay in the repos.
"""

from __future__ import annotations

import os
from pathlib import Path

STATE_DIR_MODE = 0o700
PRIVATE_FILE_MODE = 0o600

STATE_DIR = Path(os.environ.get("MACRO_E2E_STATE_DIR", "~/.local/state/macro-e2e")).expanduser()
WALLETS_BACK_DIR = Path(__file__).resolve().parents[2]

API_BINARY = STATE_DIR / "bin" / "waas-e2e-bin"
API_ENVIRON = STATE_DIR / "wallets-api.environ"
API_PID_FILE = STATE_DIR / "run" / "wallets-api.pid"
API_LOG = STATE_DIR / "logs" / "wallets-api.log"
API_HEALTH_URL = "http://127.0.0.1:2002/health"

LOCKS_DIR = STATE_DIR / "locks"
FUNDING_DIR = STATE_DIR / "funding"
FUNDING_LEDGER = FUNDING_DIR / "ledger.json"
SECRETS_DIR = STATE_DIR / "secrets"
BACKUPS_DIR = STATE_DIR / "backups"
OUTCOMES_DIR = STATE_DIR / "outcomes"
LOGS_DIR = STATE_DIR / "logs"

MARKETS_ADMIN_PASSWORD_FILE = SECRETS_DIR / "markets-e2e-admin-password.gpg"
RECORDING_LOCK = STATE_DIR / "custody-e2e-recording.lock"
ENV_READY_MARKER = STATE_DIR / "env-ready"


def ensure_private_dir(path: Path) -> Path:
    """Creates path (and parents under the state dir) with mode 0700."""
    path.mkdir(parents=True, exist_ok=True, mode=STATE_DIR_MODE)
    os.chmod(path, STATE_DIR_MODE)
    return path


def write_private_file(path: Path, data: bytes) -> None:
    """Writes data atomically to path with mode 0600."""
    ensure_private_dir(path.parent)
    partial = path.with_name(f".{path.name}.partial")
    fd = os.open(partial, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, PRIVATE_FILE_MODE)
    try:
        os.write(fd, data)
    finally:
        os.close(fd)
    os.replace(partial, path)


def read_api_environ() -> dict[str, str]:
    """The NUL-separated KEY=VALUE environment the Wallets API runs with."""
    if not API_ENVIRON.is_file():
        raise SystemExit(f"{API_ENVIRON} is missing; run scripts/e2e/capture_api_env.py first")
    environment: dict[str, str] = {}
    for entry in API_ENVIRON.read_bytes().split(b"\0"):
        if b"=" not in entry:
            continue
        key, value = entry.split(b"=", 1)
        environment[key.decode()] = value.decode()
    return environment
