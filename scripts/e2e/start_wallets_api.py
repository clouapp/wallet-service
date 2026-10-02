#!/usr/bin/env python3
"""Builds, stops and starts the e2e Wallets API from the persistent state dir.

  --build    go build into <state dir>/bin/waas-e2e-bin; the previous binary is kept as
             waas-e2e-bin.prev-<UTC timestamp>
  --status   print the running PID, binary and health; change nothing
  (default)  stop the running e2e API (SIGTERM, SIGKILL on that PID only if SIGTERM is
             ignored) and start the binary with <state dir>/wallets-api.environ

The PID lives in <state dir>/run/wallets-api.pid; output goes to <state dir>/logs/wallets-api.log.
A process is only stopped when its executable is the state-dir binary.

Usage: scripts/e2e/start_wallets_api.py [--build] [--status] [--no-start]
"""

from __future__ import annotations

import argparse
import datetime as dt
import os
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from e2e_state import (  # noqa: E402
    API_BINARY,
    API_HEALTH_URL,
    API_LOG,
    API_PID_FILE,
    WALLETS_BACK_DIR,
    ensure_private_dir,
    read_api_environ,
    write_private_file,
)

SIGTERM_GRACE_SECONDS = 20
HEALTH_TIMEOUT_SECONDS = 60
HEALTH_POLL_SECONDS = 1
HTTP_OK = 200
BUILD_TIMEOUT_SECONDS = 600


def utc_stamp() -> str:
    return dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ")


def health_status() -> int | None:
    try:
        with urllib.request.urlopen(API_HEALTH_URL, timeout=3) as response:
            return response.status
    except urllib.error.HTTPError as error:
        return error.code
    except (urllib.error.URLError, OSError):
        return None


def process_executable(pid: int) -> str | None:
    try:
        return os.readlink(f"/proc/{pid}/exe").removesuffix(" (deleted)")
    except OSError:
        return None


def running_pid() -> int | None:
    """PID from the pid file when that process still runs the state-dir binary."""
    if not API_PID_FILE.is_file():
        return None
    try:
        pid = int(API_PID_FILE.read_text().strip())
    except ValueError:
        return None
    executable = process_executable(pid)
    if executable is None:
        return None
    if Path(executable) != API_BINARY and not Path(executable).name.startswith(API_BINARY.name):
        raise SystemExit(f"PID {pid} from {API_PID_FILE} runs {executable}, not {API_BINARY}; not touching it")
    return pid


def build() -> None:
    ensure_private_dir(API_BINARY.parent)
    fresh = API_BINARY.with_name(f"{API_BINARY.name}.new")
    print(f"building {fresh} ...", flush=True)
    subprocess.run(["go", "build", "-o", str(fresh), "."], cwd=WALLETS_BACK_DIR, check=True, timeout=BUILD_TIMEOUT_SECONDS)
    if API_BINARY.exists():
        previous = API_BINARY.with_name(f"{API_BINARY.name}.prev-{utc_stamp()}")
        shutil.copy2(API_BINARY, previous)
        print(f"previous binary kept as {previous}")
    os.replace(fresh, API_BINARY)
    print(f"installed {API_BINARY}")


def stop(pid: int) -> None:
    print(f"stopping PID {pid} (SIGTERM)", flush=True)
    os.kill(pid, signal.SIGTERM)
    deadline = time.monotonic() + SIGTERM_GRACE_SECONDS
    while time.monotonic() < deadline:
        if process_executable(pid) is None:
            print(f"PID {pid} exited")
            return
        time.sleep(1)
    print(f"PID {pid} ignored SIGTERM for {SIGTERM_GRACE_SECONDS}s; SIGKILL", flush=True)
    os.kill(pid, signal.SIGKILL)
    time.sleep(1)


def start() -> int:
    if not API_BINARY.is_file():
        raise SystemExit(f"{API_BINARY} is missing; run with --build")
    if health_status() is not None:
        raise SystemExit(f"{API_HEALTH_URL} already answers; another API holds the port")
    environment = read_api_environ()
    ensure_private_dir(API_LOG.parent)
    with open(API_LOG, "ab") as log:
        process = subprocess.Popen(
            [str(API_BINARY)],
            cwd=WALLETS_BACK_DIR,
            env=environment,
            stdin=subprocess.DEVNULL,
            stdout=log,
            stderr=log,
            start_new_session=True,
        )
    write_private_file(API_PID_FILE, f"{process.pid}\n".encode())
    deadline = time.monotonic() + HEALTH_TIMEOUT_SECONDS
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise SystemExit(f"API exited with {process.returncode}; see {API_LOG}")
        if health_status() == HTTP_OK:
            print(f"started PID {process.pid}; health {HTTP_OK}")
            return process.pid
        time.sleep(HEALTH_POLL_SECONDS)
    raise SystemExit(f"API PID {process.pid} not healthy after {HEALTH_TIMEOUT_SECONDS}s; see {API_LOG}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--build", action="store_true", help="rebuild the binary first")
    parser.add_argument("--status", action="store_true", help="report only")
    parser.add_argument("--no-start", action="store_true", help="stop (and build) without starting")
    args = parser.parse_args()

    pid = running_pid()
    if args.status:
        print(f"pid={pid or '-'} binary={API_BINARY} health={health_status() or '-'} log={API_LOG}")
        return 0
    if args.build:
        build()
    if pid is not None:
        stop(pid)
    if not args.no_start:
        start()
    return 0


if __name__ == "__main__":
    sys.exit(main())
