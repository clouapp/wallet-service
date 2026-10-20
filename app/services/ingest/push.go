package ingest

import (
	"context"
	"log/slog"
	"time"
)

// pushWriteTimeout bounds the note of one received webhook.
const pushWriteTimeout = 2 * time.Second

// PushRecorder notes when a provider webhook for a chain was received, for the
// health check. The provider supplies it; this package never imports the Redis client.
type PushRecorder interface {
	RecordPush(ctx context.Context, chainID string, at time.Time) error
}

// SetPushRecorder wires where received provider webhooks are noted; without one
// none is.
func (s *Service) SetPushRecorder(pushes PushRecorder) {
	s.pushes = pushes
}

// recordPush notes a webhook of chainID received now. A note that fails is logged
// and never fails the webhook: its transfers are already recorded.
func (s *Service) recordPush(ctx context.Context, chainID string) {
	if s.pushes == nil {
		return
	}
	recordCtx, cancel := context.WithTimeout(ctx, pushWriteTimeout)
	defer cancel()
	if err := s.pushes.RecordPush(recordCtx, chainID, time.Now().UTC()); err != nil {
		slog.Warn("provider push not recorded", "chain", chainID, "error", err)
	}
}
