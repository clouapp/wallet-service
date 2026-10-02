#!/opt/code/localstack/.venv/bin/python
"""Encrypted Secrets Manager snapshots for the community LocalStack image.

The community image has no native persistence, so MPC share B (vault/wallet/<id>/share-b)
would vanish on every container restart. This tool runs inside the LocalStack container:

  export   snapshot every secret to /var/lib/localstack/secrets-snapshot (gpg, atomic, rotated)
  restore  recreate secrets missing from LocalStack using the newest readable snapshot;
           never overwrites, keeps the original ARN suffix (moto `_custom_id_` tag)
  loop     export every SECRETS_SNAPSHOT_INTERVAL seconds (started by the ready.d hook)

The shutdown.d hook calls export_in_process(), which reads the moto backend directly because
the gateway already answers 503 while LocalStack shuts down.

Snapshots only grow: a secret missing live is carried over from the previous snapshot unless
SECRETS_SNAPSHOT_FORCE=1. Only names and counts are ever logged; plaintext exists only in
memory and on gpg stdin.
"""

from __future__ import annotations

import base64
import binascii
import datetime as dt
import fcntl
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any, Callable

import boto3
from botocore.exceptions import BotoCoreError, ClientError

ENDPOINT_URL = "http://localhost:4566"
REGION = os.environ.get("AWS_DEFAULT_REGION", "us-east-1")
ACCOUNT_ID = os.environ.get("SECRETS_SNAPSHOT_ACCOUNT_ID", "000000000000")
SNAPSHOT_DIR = Path(os.environ.get("SECRETS_SNAPSHOT_DIR", "/var/lib/localstack/secrets-snapshot"))
KEY_FILE = Path(os.environ.get("SECRETS_SNAPSHOT_KEY_FILE", "/run/secrets/localstack-seed/seed.key"))
INTERVAL_SECONDS = int(os.environ.get("SECRETS_SNAPSHOT_INTERVAL", "60"))
KEEP_SNAPSHOTS = int(os.environ.get("SECRETS_SNAPSHOT_KEEP", "20"))
FORCE_EXPORT = os.environ.get("SECRETS_SNAPSHOT_FORCE") == "1"

SNAPSHOT_PREFIX = "secrets-"
SNAPSHOT_SUFFIX = ".json.gpg"
META_SUFFIX = ".meta.json"
EXPORT_LOCK = SNAPSHOT_DIR / ".export.lock"
LOOP_LOCK = Path("/tmp/secrets-snapshot-loop.lock")
RESTORED_MARKER = Path("/tmp/secrets-snapshot.restored")
CUSTOM_ID_TAG = "_custom_id_"
ARN_SUFFIX_RE = re.compile(r"-([A-Za-z]{6})$")
MIN_KEY_BYTES = 32
LIST_PAGE_SIZE = 100
CLIENT_TOKEN_LENGTHS = range(32, 65)

EXIT_OK = 0
EXIT_FAILED = 1
EXIT_REFUSED = 3

GPG_BASE = ("gpg", "--batch", "--yes", "--quiet", "--no-tty", "--no-symkey-cache", "--pinentry-mode", "loopback")
GPG_ENCRYPT = ("--symmetric", "--cipher-algo", "AES256", "--s2k-mode", "3", "--s2k-digest-algo", "SHA512",
               "--s2k-count", "65011712")


class SnapshotError(Exception):
    """A failure whose message is safe to log (never contains secret material)."""


def log(message: str) -> None:
    stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    print(f"{stamp} secrets-snapshot: {message}", flush=True)


def client():
    return boto3.client(
        "secretsmanager",
        endpoint_url=ENDPOINT_URL,
        region_name=REGION,
        aws_access_key_id="test",
        aws_secret_access_key="test",
    )


def boot_id() -> str:
    """Start time of PID 1; changes on every container (re)start."""
    return Path("/proc/1/stat").read_text().rsplit(")", 1)[1].split()[19]


def restored_in_this_run() -> bool:
    return RESTORED_MARKER.is_file() and RESTORED_MARKER.read_text().strip() == boot_id()


def require_key() -> None:
    if not KEY_FILE.is_file():
        raise SnapshotError(f"seed key {KEY_FILE} is missing; snapshots disabled")
    if KEY_FILE.stat().st_size < MIN_KEY_BYTES:
        raise SnapshotError(f"seed key {KEY_FILE} is too short")


# ---------------------------------------------------------------- gpg / files


def gpg(args: list[str], stdin: bytes | None) -> bytes:
    home = tempfile.mkdtemp(prefix="gnupg-")
    try:
        result = subprocess.run(
            [*GPG_BASE, "--homedir", home, "--passphrase-file", str(KEY_FILE), *args],
            input=stdin,
            capture_output=True,
        )
    finally:
        shutil.rmtree(home, ignore_errors=True)
    if result.returncode != 0:
        raise SnapshotError(f"gpg exited with {result.returncode}")
    return result.stdout


def write_atomic(path: Path, data: bytes) -> None:
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix=".tmp-")
    try:
        with os.fdopen(fd, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(tmp, 0o600)
        os.replace(tmp, path)
    except BaseException:
        Path(tmp).unlink(missing_ok=True)
        raise
    dir_fd = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(dir_fd)
    finally:
        os.close(dir_fd)


# ---------------------------------------------------------------- secret entries


def material(entry: dict[str, Any]) -> list[Any]:
    return [entry.get("secret_binary_b64"), entry.get("secret_string")]


def digest_of(secrets: list[dict[str, Any]]) -> str:
    canonical = json.dumps(sorted([s["name"], s["arn"], *material(s)] for s in secrets), separators=(",", ":"))
    return hashlib.sha256(canonical.encode()).hexdigest()


def without_custom_id(tags: list[dict[str, str]] | None) -> list[dict[str, str]]:
    return [dict(tag) for tag in tags or [] if tag.get("Key") != CUSTOM_ID_TAG]


def collect_via_api() -> list[dict[str, Any]]:
    sm = client()
    names: list[str] = []
    for page in sm.get_paginator("list_secrets").paginate(PaginationConfig={"PageSize": LIST_PAGE_SIZE}):
        names.extend(entry["Name"] for entry in page.get("SecretList", []) if not entry.get("DeletedDate"))
    return [read_live_secret(sm, name) for name in sorted(names)]


def read_live_secret(sm, name: str) -> dict[str, Any]:
    described = sm.describe_secret(SecretId=name)
    value = sm.get_secret_value(SecretId=name)
    binary = value.get("SecretBinary")
    return {
        "name": name,
        "arn": value["ARN"],
        "version_id": value.get("VersionId"),
        "description": described.get("Description"),
        "tags": without_custom_id(described.get("Tags")),
        "secret_binary_b64": base64.b64encode(binary).decode() if binary is not None else None,
        "secret_string": value.get("SecretString"),
    }


def binary_as_b64(value: Any) -> str | None:
    """moto keeps SecretBinary either as raw bytes or as the base64 text from the wire."""
    if value is None:
        return None
    if isinstance(value, (bytes, bytearray)):
        return base64.b64encode(bytes(value)).decode()
    if isinstance(value, str):
        try:
            base64.b64decode(value, validate=True)
        except binascii.Error as error:
            raise SnapshotError("moto holds a SecretBinary that is not base64 text") from error
        return value
    raise SnapshotError(f"unexpected SecretBinary type {type(value).__name__}")


def collect_in_process() -> list[dict[str, Any]]:
    from moto.secretsmanager.models import secretsmanager_backends

    backend = secretsmanager_backends[ACCOUNT_ID][REGION]
    entries = []
    for secret in list(backend.secrets.values()):
        if not hasattr(secret, "versions") or secret.is_deleted():
            continue
        version = secret.versions.get(secret.default_version_id) or {}
        if "secret_binary" not in version and "secret_string" not in version:
            raise SnapshotError(f"secret {secret.name} has no current value")
        entries.append({
            "name": secret.name,
            "arn": secret.arn,
            "version_id": version.get("version_id"),
            "description": secret.description,
            "tags": without_custom_id(secret.tags),
            "secret_binary_b64": binary_as_b64(version.get("secret_binary")),
            "secret_string": version.get("secret_string"),
        })
    return sorted(entries, key=lambda entry: entry["name"])


# ---------------------------------------------------------------- snapshot files


def snapshot_metas() -> list[tuple[Path, dict[str, Any]]]:
    """Newest first; only metas whose encrypted snapshot exists."""
    found = []
    for meta_path in sorted(SNAPSHOT_DIR.glob(f"{SNAPSHOT_PREFIX}*{META_SUFFIX}"), reverse=True):
        snapshot_path = meta_path.with_name(meta_path.name.removesuffix(META_SUFFIX) + SNAPSHOT_SUFFIX)
        if not snapshot_path.exists():
            continue
        try:
            found.append((snapshot_path, json.loads(meta_path.read_text())))
        except (OSError, json.JSONDecodeError):
            log(f"ignoring unreadable meta {meta_path.name}")
    return found


def load_snapshot(snapshot_path: Path, meta: dict[str, Any]) -> list[dict[str, Any]]:
    try:
        record = json.loads(gpg(["--decrypt", str(snapshot_path)], None))
    except json.JSONDecodeError as error:
        raise SnapshotError(f"{snapshot_path.name} does not hold JSON") from error
    secrets = record.get("secrets")
    if not isinstance(secrets, list):
        raise SnapshotError(f"{snapshot_path.name} has no secrets list")
    if len(secrets) != meta.get("count") or digest_of(secrets) != meta.get("digest"):
        raise SnapshotError(f"{snapshot_path.name} does not match its meta (count/digest)")
    return secrets


def newest_readable_snapshot() -> tuple[str, dict[str, Any], list[dict[str, Any]]] | None:
    metas = snapshot_metas()
    if not metas:
        return None
    for snapshot_path, meta in metas:
        try:
            return snapshot_path.name, meta, load_snapshot(snapshot_path, meta)
        except SnapshotError as error:
            log(f"skipping snapshot {snapshot_path.name}: {error}")
    raise SnapshotError(f"none of the {len(metas)} snapshot(s) could be read")


def rotate() -> None:
    snapshots = sorted(SNAPSHOT_DIR.glob(f"{SNAPSHOT_PREFIX}*{SNAPSHOT_SUFFIX}"), reverse=True)
    for snapshot_path in snapshots[KEEP_SNAPSHOTS:]:
        snapshot_path.unlink(missing_ok=True)
        snapshot_path.with_name(snapshot_path.name.removesuffix(SNAPSHOT_SUFFIX) + META_SUFFIX).unlink(missing_ok=True)


# ---------------------------------------------------------------- export


def check_version_invariant(live: list[dict[str, Any]], previous: list[dict[str, Any]]) -> None:
    """A Secrets Manager version is immutable: same ARN + VersionId must mean the same value."""
    known = {(entry["arn"], entry.get("version_id")): material(entry) for entry in previous}
    mismatched = [entry["name"] for entry in live
                  if known.get((entry["arn"], entry.get("version_id")), material(entry)) != material(entry)]
    if mismatched:
        raise SnapshotError(f"refusing export: {len(mismatched)} secret(s) changed value under the same "
                            f"VersionId (collector mismatch?), e.g. {mismatched[:3]}")


def merge_with_previous(live: list[dict[str, Any]], previous: list[dict[str, Any]]) -> list[dict[str, Any]]:
    live_names = {entry["name"] for entry in live}
    carried = [entry for entry in previous if entry["name"] not in live_names]
    if carried and not FORCE_EXPORT:
        log(f"{len(carried)} secret(s) missing live are kept from the previous snapshot, e.g. "
            f"{[entry['name'] for entry in carried[:3]]}")
        return sorted([*live, *carried], key=lambda entry: entry["name"])
    return live


def export_with(collect: Callable[[], list[dict[str, Any]]]) -> int:
    require_key()
    if not restored_in_this_run():
        log("export skipped: restore has not completed in this container run")
        return EXIT_REFUSED
    SNAPSHOT_DIR.mkdir(mode=0o700, parents=True, exist_ok=True)
    with open(EXPORT_LOCK, "w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        live = collect()
        metas = snapshot_metas()
        previous_meta = metas[0][1] if metas else None
        if previous_meta and previous_meta.get("digest") == digest_of(live):
            return EXIT_OK
        previous_secrets: list[dict[str, Any]] = []
        if metas:
            _, previous_meta, previous_secrets = newest_readable_snapshot()
        check_version_invariant(live, previous_secrets)
        secrets = merge_with_previous(live, previous_secrets)
        digest = digest_of(secrets)
        if previous_meta and previous_meta.get("digest") == digest:
            return EXIT_OK
        stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
        snapshot_path = SNAPSHOT_DIR / f"{SNAPSHOT_PREFIX}{stamp}{SNAPSHOT_SUFFIX}"
        record = {"schema_version": 1, "account_id": ACCOUNT_ID, "region": REGION, "secrets": secrets}
        write_atomic(snapshot_path, gpg([*GPG_ENCRYPT, "--output", "-"], json.dumps(record).encode()))
        meta = {"count": len(secrets), "live_count": len(live), "digest": digest, "created_at": stamp}
        write_atomic(snapshot_path.with_name(f"{SNAPSHOT_PREFIX}{stamp}{META_SUFFIX}"), json.dumps(meta).encode())
        log(f"exported {len(secrets)} secret(s) ({len(live)} live) to {snapshot_path.name}")
        rotate()
    return EXIT_OK


def cmd_export() -> int:
    return export_with(collect_via_api)


def export_in_process() -> int:
    """Entry point for the shutdown.d hook, which LocalStack exec()s inside its own process."""
    try:
        return export_with(collect_in_process)
    except SnapshotError as error:
        log(f"shutdown export failed: {error}")
        return EXIT_FAILED


# ---------------------------------------------------------------- restore


def create_with_original_arn(sm, entry: dict[str, Any]) -> str:
    suffix = ARN_SUFFIX_RE.search(entry["arn"] or "")
    tags = list(entry.get("tags") or [])
    if suffix:
        tags.append({"Key": CUSTOM_ID_TAG, "Value": suffix.group(1)})
    request: dict[str, Any] = {"Name": entry["name"], "Tags": tags}
    if entry.get("description"):
        request["Description"] = entry["description"]
    if entry.get("secret_binary_b64") is not None:
        request["SecretBinary"] = base64.b64decode(entry["secret_binary_b64"])
    else:
        request["SecretString"] = entry["secret_string"]
    version_id = entry.get("version_id") or ""
    if len(version_id) in CLIENT_TOKEN_LENGTHS:
        request["ClientRequestToken"] = version_id
    created = sm.create_secret(**request)
    if suffix:
        sm.untag_resource(SecretId=created["ARN"], TagKeys=[CUSTOM_ID_TAG])
    return created["ARN"]


def restore_entry(sm, entry: dict[str, Any]) -> str:
    try:
        live = read_live_secret(sm, entry["name"])
    except ClientError as error:
        if error.response.get("Error", {}).get("Code") != "ResourceNotFoundException":
            raise
        arn = create_with_original_arn(sm, entry)
        return "restored" if arn == entry["arn"] else "restored-new-arn"
    if material(live) != material(entry):
        return "different"
    return "present" if live["arn"] == entry["arn"] else "present-other-arn"


def cmd_restore() -> int:
    require_key()
    RESTORED_MARKER.unlink(missing_ok=True)
    SNAPSHOT_DIR.mkdir(mode=0o700, parents=True, exist_ok=True)
    found = newest_readable_snapshot()
    if found is None:
        log("no snapshot yet; nothing to restore")
        RESTORED_MARKER.write_text(boot_id())
        return EXIT_OK
    name, _, secrets = found
    sm = client()
    outcomes: dict[str, list[str]] = {}
    for entry in secrets:
        try:
            outcome = restore_entry(sm, entry)
        except ClientError as error:
            outcome = f"failed:{error.response.get('Error', {}).get('Code', 'unknown')}"
        outcomes.setdefault(outcome, []).append(entry["name"])
    for outcome, secret_names in sorted(outcomes.items()):
        if outcome in ("restored", "present"):
            continue
        for secret_name in secret_names:
            log(f"{outcome}: {secret_name} (live value kept)" if outcome == "different" else f"{outcome}: {secret_name}")
    summary = ", ".join(f"{k}={len(v)}" for k, v in sorted(outcomes.items()))
    log(f"restore from {name}: {len(secrets)} in snapshot; {summary}")
    if any(outcome.startswith("failed") for outcome in outcomes):
        return EXIT_FAILED
    RESTORED_MARKER.write_text(boot_id())
    return EXIT_OK


# ---------------------------------------------------------------- loop


def cmd_loop() -> int:
    lock = open(LOOP_LOCK, "w")
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        log("loop already running")
        return EXIT_OK
    log(f"periodic export every {INTERVAL_SECONDS}s")
    while True:
        time.sleep(INTERVAL_SECONDS)
        try:
            cmd_export()
        except (SnapshotError, ClientError, BotoCoreError, OSError) as error:
            log(f"periodic export failed: {type(error).__name__}: {str(error)[:200]}")


COMMANDS = {"export": cmd_export, "restore": cmd_restore, "loop": cmd_loop}


def main() -> int:
    os.umask(0o077)
    if len(sys.argv) != 2 or sys.argv[1] not in COMMANDS:
        print(__doc__)
        return 2
    try:
        return COMMANDS[sys.argv[1]]()
    except SnapshotError as error:
        log(f"{sys.argv[1]} failed: {error}")
        return EXIT_FAILED
    except ClientError as error:
        log(f"{sys.argv[1]} failed: AWS error {error.response.get('Error', {}).get('Code', 'unknown')}")
        return EXIT_FAILED
    except BotoCoreError as error:
        log(f"{sys.argv[1]} failed: {type(error).__name__}")
        return EXIT_FAILED


if __name__ == "__main__":
    sys.exit(main())
