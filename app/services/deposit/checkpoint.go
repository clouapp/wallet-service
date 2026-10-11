package deposit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/macrowallets/waas/app/models"
)

// errNoCheckpointStore means the service was built without the durable checkpoint store.
var errNoCheckpointStore = errors.New("deposit: no checkpoint store configured")

// CheckpointStore keeps each chain's scan checkpoint in the database: the last block
// whose deposits were all recorded or saved as pending. Redis only caches it.
type CheckpointStore interface {
	// Find returns the chain's checkpoint, or models.ErrRepositoryNotFound for a chain
	// never scanned.
	Find(ctx context.Context, chainID string) (uint64, error)
	// Advance moves the chain's checkpoint to block, never back, and returns the
	// checkpoint now stored.
	Advance(ctx context.Context, chainID string, block uint64) (uint64, error)
}

func checkpointCacheKey(chainID string) string {
	return "vault:checkpoint:" + chainID
}

// loadCheckpoint returns the block the chain's scan resumes after: the Redis cache
// when it holds one, else the database. found is false only for a chain the database
// does not hold while Redis answered a clean miss (or is not configured); a Redis error
// with no database row fails instead, so a cache outage never sends the scan to the head.
func (s *Service) loadCheckpoint(ctx context.Context, chainID string) (uint64, bool, error) {
	if s.checkpoints == nil {
		return 0, false, errNoCheckpointStore
	}
	cacheFailed := false
	if s.store != nil {
		cached, found, err := s.store.Uint64(ctx, checkpointCacheKey(chainID))
		switch {
		case err != nil:
			cacheFailed = true
			slog.Warn("deposit checkpoint cache unavailable, reading the database", "chain", chainID, "error", err)
		case found:
			return s.persistCachedCheckpoint(ctx, chainID, cached), true, nil
		}
	}
	stored, err := s.checkpoints.Find(ctx, chainID)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		if cacheFailed {
			return 0, false, fmt.Errorf("read %s checkpoint: the cache failed and the database holds none, so the chain cannot be told from a new one", chainID)
		}
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read %s checkpoint: %w", chainID, err)
	}
	s.persisted.Store(chainID, struct{}{})
	if s.store != nil && !cacheFailed {
		s.cacheCheckpoint(ctx, chainID, stored)
	}
	return stored, true, nil
}

// persistCachedCheckpoint copies a checkpoint read from Redis to the database once per
// process, and again after a Redis write failed, so a cache written before the database
// held checkpoints cannot reset the chain to the head when the key is lost. It returns
// the block to resume after: the database one, which is never behind the cache.
func (s *Service) persistCachedCheckpoint(ctx context.Context, chainID string, cached uint64) uint64 {
	if _, done := s.persisted.Load(chainID); done {
		return cached
	}
	stored, err := s.checkpoints.Advance(ctx, chainID, cached)
	if err != nil {
		slog.Warn("deposit checkpoint not copied to the database; Redis keeps serving it", "chain", chainID, "block", cached, "error", err)
		return cached
	}
	s.persisted.Store(chainID, struct{}{})
	if stored != cached {
		s.cacheCheckpoint(ctx, chainID, stored)
	}
	return stored
}

// saveCheckpoint moves the chain's checkpoint to block in the database, then caches
// what the database stored. The database never moves back, so a slower scanner cannot
// rewind it, and Redis is written only after the database, so it never holds a block
// the database lacks.
func (s *Service) saveCheckpoint(ctx context.Context, chainID string, block uint64) error {
	if s.checkpoints == nil {
		return errNoCheckpointStore
	}
	stored, err := s.checkpoints.Advance(ctx, chainID, block)
	if err != nil {
		return fmt.Errorf("save %s checkpoint %d: %w", chainID, block, err)
	}
	s.persisted.Store(chainID, struct{}{})
	if s.store == nil {
		return nil
	}
	if err := s.store.Set(ctx, checkpointCacheKey(chainID), stored, 0); err != nil {
		// Redis now trails the database. The next read compares the two, so a Redis
		// that keeps refusing writes does not make every cycle repeat the same blocks.
		s.persisted.Delete(chainID)
		return fmt.Errorf("cache %s checkpoint %d: %w", chainID, stored, err)
	}
	return nil
}

// cacheCheckpoint refreshes the Redis copy; a failure only costs the next read a
// database query.
func (s *Service) cacheCheckpoint(ctx context.Context, chainID string, block uint64) {
	if err := s.store.Set(ctx, checkpointCacheKey(chainID), block, 0); err != nil {
		slog.Warn("deposit checkpoint cache not refreshed", "chain", chainID, "block", block, "error", err)
	}
}
