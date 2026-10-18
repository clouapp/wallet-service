package deposit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/macrowallets/waas/app/models"
)

// CheckpointCopy is one store's copy of a chain's scan checkpoint.
type CheckpointCopy struct {
	Block uint64
	Found bool
}

// CheckpointMove is one scan:deposits --set-checkpoint run: the chain head, the copies
// the database and Redis held before it, and the block it moves them to.
type CheckpointMove struct {
	ChainID string
	Head    uint64
	Target  uint64
	Stored  CheckpointCopy
	Cached  CheckpointCopy
	Applied bool
	// CacheRefreshed is false when the database was written and Redis was not (or is
	// not configured); the next scan then reads the database.
	CacheRefreshed bool
}

// SetCheckpoint moves the chain's checkpoint to block, back or forward, in the database
// and in Redis, and refuses a block past the chain head with ErrCheckpointAheadOfHead.
// Without apply it reads and reports only; with apply it holds the chain's scan lock,
// so no scan cycle runs between the read and the write.
func (s *Service) SetCheckpoint(ctx context.Context, chainID string, block uint64, apply bool) (CheckpointMove, error) {
	move := CheckpointMove{ChainID: chainID, Target: block}
	if s.checkpoints == nil {
		return move, errNoCheckpointStore
	}
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return move, err
	}
	if apply {
		release, lockErr := s.lockChain(chainID)
		if lockErr != nil {
			return move, lockErr
		}
		defer release()
	}
	if move.Stored, move.Cached, err = s.checkpointCopies(ctx, chainID); err != nil {
		return move, err
	}
	if move.Head, err = adapter.GetLatestBlock(ctx); err != nil {
		return move, fmt.Errorf("get latest block: %w", err)
	}
	if block > move.Head {
		return move, fmt.Errorf("%w: block %d is past the %s head %d; pick a block at or below the head", ErrCheckpointAheadOfHead, block, chainID, move.Head)
	}
	if !apply {
		return move, nil
	}
	if err := s.clearCachedCheckpoint(ctx, chainID); err != nil {
		return move, err
	}
	if err := s.checkpoints.Set(ctx, chainID, block); err != nil {
		return move, fmt.Errorf("set %s checkpoint %d: %w", chainID, block, err)
	}
	move.Applied = true
	if s.store != nil && s.cacheCheckpoint(ctx, chainID, block) {
		move.CacheRefreshed = true
		s.cacheInSync.Store(chainID, struct{}{})
	}
	slog.Warn("deposit scan checkpoint set by the operator", "chain", chainID, "from", move.Stored.Block, "to", block, "head", move.Head)
	return move, nil
}

// Checkpoint returns the chain's checkpoint without writing anything: the database
// row, else the Redis copy; found is false for a chain neither holds.
func (s *Service) Checkpoint(ctx context.Context, chainID string) (uint64, bool, error) {
	if s.checkpoints == nil {
		return 0, false, errNoCheckpointStore
	}
	stored, cached, err := s.checkpointCopies(ctx, chainID)
	if err != nil {
		return 0, false, err
	}
	current := preferStored(stored, cached)
	return current.Block, current.Found, nil
}

// DropCheckpoint removes the chain's checkpoint from Redis and the database under the
// chain's scan lock, so the next scan starts at the head of the network the chain's RPC
// serves. It returns the checkpoint dropped (the database row, else the Redis copy). A
// lock another scanner holds is ErrScanInProgress, and nothing is dropped.
func (s *Service) DropCheckpoint(ctx context.Context, chainID string) (uint64, bool, error) {
	if s.checkpoints == nil {
		return 0, false, errNoCheckpointStore
	}
	release, err := s.lockChain(chainID)
	if err != nil {
		return 0, false, err
	}
	defer release()
	stored, cached, err := s.checkpointCopies(ctx, chainID)
	if err != nil {
		return 0, false, err
	}
	if err := s.clearCachedCheckpoint(ctx, chainID); err != nil {
		return 0, false, err
	}
	if err := s.checkpoints.Delete(ctx, chainID); err != nil {
		return 0, false, fmt.Errorf("delete %s checkpoint: %w", chainID, err)
	}
	dropped := preferStored(stored, cached)
	slog.Warn("deposit scan checkpoint dropped; the next scan starts at the chain head", "chain", chainID, "block", dropped.Block, "found", dropped.Found)
	return dropped.Block, dropped.Found, nil
}

// checkpointCopies reads the chain's checkpoint from the database and from Redis
// without writing either; a store that cannot be read fails the call.
func (s *Service) checkpointCopies(ctx context.Context, chainID string) (CheckpointCopy, CheckpointCopy, error) {
	var stored, cached CheckpointCopy
	block, err := s.checkpoints.Find(ctx, chainID)
	switch {
	case err == nil:
		stored = CheckpointCopy{Block: block, Found: true}
	case !errors.Is(err, models.ErrRepositoryNotFound):
		return stored, cached, fmt.Errorf("read %s checkpoint: %w", chainID, err)
	}
	if s.store != nil {
		block, found, err := s.store.Uint64(ctx, checkpointCacheKey(chainID))
		if err != nil {
			return stored, cached, fmt.Errorf("read %s checkpoint cache: %w", chainID, err)
		}
		cached = CheckpointCopy{Block: block, Found: found}
	}
	return stored, cached, nil
}

// clearCachedCheckpoint deletes the Redis copy before an operator rewrites the database
// row, so no scanner resumes from a copy the database no longer holds; a failed delete
// stops the operator before the database is touched.
func (s *Service) clearCachedCheckpoint(ctx context.Context, chainID string) error {
	s.cacheInSync.Delete(chainID)
	if s.store == nil {
		return nil
	}
	if err := s.store.Delete(ctx, checkpointCacheKey(chainID)); err != nil {
		return fmt.Errorf("clear %s checkpoint cache: %w", chainID, err)
	}
	return nil
}

// preferStored returns the database copy when there is one, else the Redis copy.
func preferStored(stored, cached CheckpointCopy) CheckpointCopy {
	if stored.Found {
		return stored
	}
	return cached
}
