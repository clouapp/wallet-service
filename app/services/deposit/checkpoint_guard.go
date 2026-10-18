package deposit

import (
	"errors"
	"fmt"
	"log/slog"
)

// ErrCheckpointAheadOfHead means a checkpoint is ahead of the chain head: further than
// a lagging RPC explains when the scanner reads it, or at all when an operator asks to
// set it.
var ErrCheckpointAheadOfHead = errors.New("deposit: scan checkpoint is ahead of the chain head")

// checkpointAheadTolerance is how many blocks a stored checkpoint may be ahead of the
// head the RPC reports before the scan cycle fails instead of waiting. A load-balanced
// RPC can answer a cycle from a node a few blocks behind the one that answered the
// previous cycle, so a checkpoint slightly ahead is normal and the cycle waits quietly
// for the head to pass it. 1000 blocks is several minutes on the fastest chains scanned
// (Arbitrum about 4 blocks a second, Solana 2.5 slots a second), more than a node still
// in rotation trails by, while a checkpoint written on another network is hundreds of
// thousands of blocks away (btc on 2026-10-18: a testnet3 checkpoint of 5,158,070
// against a testnet4 head of 156,878). It is generous on slow chains (a week of
// Bitcoin), which is why scan:deposits --set-checkpoint refuses any block past the head
// instead of relying on this guard. Required confirmations are no measure of RPC lag
// (Solana needs 1), so the tolerance is one constant.
const checkpointAheadTolerance uint64 = 1000

// checkCheckpointNotAhead returns nil for a checkpoint at the head or ahead of it by
// at most checkpointAheadTolerance, and ErrCheckpointAheadOfHead, logged at error
// level, past that. It never moves the checkpoint: moving it could skip deposits.
func checkCheckpointNotAhead(chainID string, checkpoint, head uint64) error {
	if checkpoint <= head {
		return nil
	}
	gap := checkpoint - head
	if gap <= checkpointAheadTolerance {
		return nil
	}
	slog.Error("deposit scan checkpoint is ahead of the chain head; no block is scanned until it is moved",
		"chain", chainID, "checkpoint", checkpoint, "head", head, "gap", gap, "tolerance", checkpointAheadTolerance)
	return fmt.Errorf("%w: %s checkpoint %d is %d blocks past the head %d (the RPC serves another network, or the checkpoint was set past the head); move it with scan:deposits %s --set-checkpoint=<block> --apply",
		ErrCheckpointAheadOfHead, chainID, checkpoint, gap, head, chainID)
}
