package snapshot

import (
	"context"
	"time"
)

// Loop exports every Interval until ctx ends. Only one loop runs per container:
// a second one finds the loop lock taken and returns at once.
func (t *Tool) Loop(ctx context.Context) error {
	lock, acquired, err := lockFile(t.cfg.LoopLock, false)
	if err != nil {
		return err
	}
	if !acquired {
		t.log.Printf("loop already running")
		return nil
	}
	defer lock.Close()

	t.log.Printf("periodic export every %ds", int(t.cfg.Interval/time.Second))
	ticker := time.NewTicker(t.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := t.Export(ctx); err != nil {
				t.log.Printf("periodic export failed: %s", safeMessage(err))
			}
		}
	}
}
