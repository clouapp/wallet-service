# Recent deposits during historical backfill

An old block checkpoint can leave new deposits unread for days on a fast chain.
Opt-in recent scanning adds an independent durable cursor without moving or
discarding the historical checkpoint:

```dotenv
DEPOSIT_SCAN_RECENT_CHAINS=arbitrum
DEPOSIT_SCAN_RECENT_WINDOW_BLOCKS=128
```

The default chain list is empty. Restart the scanner after changing these values.
The window must be between 1 and 10,000 blocks and only sets the first recent
cursor's floor. It does not limit historical coverage.

When historical lag exceeds the chain's catch-up window, the scanner first stores
`recent:<chain>` in `deposit_scan_checkpoints`, then reads its recent window.
Subsequent cycles and process restarts resume that cursor contiguously, even after
a long outage; they never reseed it to a newer tip. Failed reads remain pending or
stop advancement, using the existing block failure policy. The same transaction
recording path deduplicates payments seen by both cursors or by provider pushes.

Recent work runs first, under a renewed shared chain lock. Once caught up to the
sampled head, one normal batch of historical work runs. If recent work is still
catching up, historical work waits for the next cycle. Pending deposits still get
a confirmation pass when recent or historical scanning fails. Reorganization
rescans cover blocks already read by either cursor.

`/health` continues reporting **historical** lag; a recent cursor does not certify
that the historical gap has closed. Logs include `historical_checkpoint`,
`recent_checkpoint` and `head`. Watch RPC capacity and pending-block alerts:
two cursors cannot compensate for an RPC that cannot keep up with new blocks.

Disabling the chain list stops recent work but retains its cursor for resumption.
Use `scan:deposits --set-checkpoint --apply` or `chains:align-network --apply` for
an intentional reset or network change. These paths clear the recent cursor as
well, under the chain lock; do not edit either cursor directly in SQL or Redis.
