#!/bin/bash
# Restore Secrets Manager (MPC share B) from the encrypted snapshot, then keep exporting it.
set -u

SNAPSHOT_TOOL=/etc/localstack/secrets-snapshot/secrets_snapshot.py
LOOP_LOG=/var/lib/localstack/logs/secrets-snapshot.log

if ! "$SNAPSHOT_TOOL" restore; then
    echo "secrets-snapshot: restore failed; periodic export NOT started (existing snapshots stay untouched)" >&2
    exit 0
fi

setsid nohup "$SNAPSHOT_TOOL" loop >>"$LOOP_LOG" 2>&1 </dev/null &
echo "secrets-snapshot: periodic export started (log: $LOOP_LOG)"
