package ingest

import (
	"context"
	"log/slog"
	"time"

	"github.com/macrowallets/waas/pkg/deadline"
)

const (
	// pushWriteBudget is how long a webhook waits for its push note: a store that
	// does not answer never slows the provider's callback down more than this.
	pushWriteBudget = 200 * time.Millisecond
	// pushWriteTimeout bounds the note itself, which may finish after the webhook
	// answered.
	pushWriteTimeout = 2 * time.Second
)

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

// recordPush notes a webhook of chainID received now. The webhook waits for the
// note at most pushWriteBudget; a note that fails, or is still running then, is
// logged and never fails the webhook: its transfers are already recorded.
func (s *Service) recordPush(ctx context.Context, chainID string) {
	if s.pushes == nil {
		return
	}
	at := time.Now().UTC()
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pushWriteTimeout)
	_, err := deadline.Wait(pushWriteBudget, func() (struct{}, error) {
		defer cancel()
		return struct{}{}, s.pushes.RecordPush(recordCtx, chainID, at)
	})
	if err != nil {
		slog.Warn("provider push not recorded", "chain", chainID, "error", err)
	}
}
