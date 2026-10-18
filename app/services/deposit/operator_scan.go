package deposit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// OperatorScan is one scan:deposits invocation. SetCheckpoint, when positive, moves
// the chain's checkpoint to that block; Apply writes it (a dry run otherwise).
type OperatorScan struct {
	ChainID       string
	Block         int64
	Tx            string
	ListPending   bool
	RetryPending  bool
	SetCheckpoint int64
	Apply         bool
}

// OperatorOutput is what scan:deposits prints before it fails or returns.
type OperatorOutput struct {
	Info []string
	Line []string
}

// RunOperator performs the one scan the artisan command asked for.
func (s *Service) RunOperator(ctx context.Context, req OperatorScan) (OperatorOutput, error) {
	var out OperatorOutput
	chainID := strings.TrimSpace(req.ChainID)
	if chainID == "" {
		return out, fmt.Errorf("chain is required")
	}
	txHash := strings.TrimSpace(req.Tx)
	if req.Block < 0 {
		return out, fmt.Errorf("--block must be positive, got %d", req.Block)
	}
	if req.SetCheckpoint < 0 {
		return out, fmt.Errorf("--set-checkpoint must be positive, got %d", req.SetCheckpoint)
	}
	if selected := countTrue(req.Block > 0, txHash != "", req.ListPending, req.RetryPending, req.SetCheckpoint > 0); selected > 1 {
		return out, fmt.Errorf("use only one of --block, --tx, --pending, --retry-pending and --set-checkpoint")
	}
	if req.Apply && req.SetCheckpoint == 0 {
		return out, fmt.Errorf("--apply only applies to --set-checkpoint")
	}
	if s == nil {
		return out, fmt.Errorf("deposit service is not initialized")
	}

	switch {
	case req.SetCheckpoint > 0:
		move, err := s.SetCheckpoint(ctx, chainID, uint64(req.SetCheckpoint), req.Apply)
		if err == nil || errors.Is(err, ErrCheckpointAheadOfHead) {
			out.Info = append(out.Info, s.describeCheckpointMove(move)...)
		}
		if err != nil {
			return out, fmt.Errorf("set checkpoint failed: %w", err)
		}
	case req.ListPending:
		entries, err := s.ListPending(ctx, chainID)
		if err != nil {
			return out, fmt.Errorf("pending list failed: %w", err)
		}
		out.Info = append(out.Info, fmt.Sprintf("pending blocks: chain=%s count=%d", chainID, len(entries)))
		for _, entry := range entries {
			out.Line = append(out.Line, fmt.Sprintf("block=%d attempts=%d error_class=%s first_failed=%s last_failed=%s next_retry=%s tx=%s error=%q",
				entry.Block, entry.Attempts, entry.ErrorClass,
				entry.FirstFailedAt.Format(time.RFC3339), entry.LastFailedAt.Format(time.RFC3339), entry.NextRetryAt.Format(time.RFC3339),
				strings.Join(entry.TxHashes, ","), entry.LastError))
		}
		return out, nil
	case req.RetryPending:
		result, err := s.ReprocessPending(ctx, chainID, true)
		if err != nil {
			return out, fmt.Errorf("pending retry failed: %w", err)
		}
		out.Info = append(out.Info, fmt.Sprintf("pending retry complete: chain=%s retried=%d resolved=%d still_pending=%d", chainID, result.Due, result.Resolved, result.StillPending))
		if result.StillPending > 0 {
			return out, fmt.Errorf("%d %s blocks are still pending", result.StillPending, chainID)
		}
	case req.Block > 0:
		recorded, err := s.ScanBlock(ctx, chainID, uint64(req.Block))
		if err != nil {
			return out, fmt.Errorf("block scan failed: %w", err)
		}
		out.Info = append(out.Info, "block scan complete: chain="+chainID+" block="+strconv.FormatInt(req.Block, 10)+" new_deposits="+strconv.Itoa(recorded))
	case txHash != "":
		recorded, err := s.ScanTransaction(ctx, chainID, txHash)
		if err != nil {
			return out, fmt.Errorf("transaction scan failed: %w", err)
		}
		out.Info = append(out.Info, "transaction scan complete: chain="+chainID+" tx="+txHash+" new_deposits="+strconv.Itoa(recorded))
	default:
		if err := s.scanLatestBlocks(ctx, chainID); err != nil {
			if errors.Is(err, ErrScanInProgress) {
				return out, fmt.Errorf("deposit scan skipped, run it again once the other scan of %s ends: %w", chainID, err)
			}
			return out, fmt.Errorf("deposit scan failed: %w", err)
		}
		out.Info = append(out.Info, "deposit scan complete: chain="+chainID)
	}
	return out, nil
}

// describeCheckpointMove prints the copies the chain held, the head, and what the run
// wrote or would write.
func (s *Service) describeCheckpointMove(move CheckpointMove) []string {
	cache := "not configured"
	if s.store != nil {
		cache = describeCheckpointCopy(move.Cached)
	}
	lines := []string{fmt.Sprintf("checkpoint of %s: database %s, cache %s, head %d",
		move.ChainID, describeCheckpointCopy(move.Stored), cache, move.Head)}
	switch {
	case move.Target > move.Head:
		return lines
	case !move.Applied:
		lines = append(lines, fmt.Sprintf("dry run: would set the %s checkpoint to %d in the database and the cache (pass --apply)", move.ChainID, move.Target))
	case move.CacheRefreshed:
		lines = append(lines, fmt.Sprintf("checkpoint of %s set to %d in the database and the cache; the next scan resumes after block %d", move.ChainID, move.Target, move.Target))
	default:
		lines = append(lines, fmt.Sprintf("checkpoint of %s set to %d in the database; the cache holds none, so the next scan reads the database and resumes after block %d", move.ChainID, move.Target, move.Target))
	}
	return lines
}

func describeCheckpointCopy(held CheckpointCopy) string {
	if !held.Found {
		return "none"
	}
	return strconv.FormatUint(held.Block, 10)
}

func countTrue(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}
