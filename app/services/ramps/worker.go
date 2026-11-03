package ramps

import (
	"context"
	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/services/webhook"
	er "github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Tick recovers stored events before processing tasks and publishing the tenant outbox.
func (s *Service) Tick(ctx context.Context, sender *webhook.Service, reconcile bool) error {
	events, err := er.NewWebhookEventRepository().FindUnprocessed(time.Now().Add(-time.Minute), 50)
	if err != nil {
		return err
	}
	for _, event := range events {
		mode, _, ok := strings.Cut(event.EventID, ":")
		if !ok || (mode != "sandbox" && mode != "production") {
			continue
		}
		if err := s.d.Tasks.Enqueue(ctx, uuid.Nil, "webhook", TaskPayload{Mode: mode, ID: event.ID}, 0); err != nil {
			return err
		}
	}
	if _, err := s.Work(ctx, 50); err != nil {
		return err
	}
	if reconcile {
		if _, err := s.Reconcile(ctx, 50); err != nil {
			return err
		} // retention scrubbing has no provider calls
		runtime, err := s.RuntimeMode(uuid.Nil, "sandbox", nil)
		if err != nil {
			return err
		}
		if _, err := runtime.Kyc.ScrubIntentMaterial(100); err != nil {
			return err
		}
		if _, err := er.NewOfframpFundingAuthorizationRepository().ClearRetainedPayloads(time.Now(), 100); err != nil {
			return err
		}
		// Old contact staging is encrypted by the engine and expires through the cache.
	}
	_, err = s.PublishOutbox(ctx, sender, 100)
	return err
}

type Worker struct{ wg sync.WaitGroup }

func (w *Worker) Wait() { w.wg.Wait() }
func (s *Service) Start(ctx context.Context, sender *webhook.Service) *Worker {
	w := &Worker{}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		timer := time.NewTicker(3 * time.Second)
		defer timer.Stop()
		last := time.Time{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				refresh := time.Since(last) > time.Minute
				if err := s.Tick(ctx, sender, refresh); err != nil && ctx.Err() == nil {
					slog.Error("ramp worker pass failed", "operation", "ramps")
				}
				if refresh {
					last = time.Now()
				}
			}
		}
	}()
	return w
}
