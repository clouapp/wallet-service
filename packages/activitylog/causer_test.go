package activitylog

import (
	"context"
	"sync"
	"testing"
)

func TestCauserRoundTrips(t *testing.T) {
	ctx := WithCauser(context.Background(), Causer{Type: "user", ID: "42", Label: "ana@acme.example"})

	got, ok := CauserFromContext(ctx)
	if !ok {
		t.Fatal("esperava ok=true")
	}
	if got.Label != "ana@acme.example" || got.ID != "42" || got.Type != "user" {
		t.Fatalf("causer alterado no trajeto: %#v", got)
	}
}

// ABSENT and empty have to be distinguishable. Under strict mode an absent
// causer REFUSES the write, while a causer filled with "" would be an
// attribution to nobody — which is precisely the defect the design rejects the
// Postgres-trigger approach over. See the spec §3.2 and §7.3.
func TestCauserAbsentIsNotAnEmptyCauser(t *testing.T) {
	if _, ok := CauserFromContext(context.Background()); ok {
		t.Fatal("an empty context must not answer ok=true")
	}

	ctx := WithCauser(context.Background(), Causer{})
	if _, ok := CauserFromContext(ctx); !ok {
		t.Fatal("an EXPLICITLY empty causer is still present")
	}
}

// An intent belongs to the NEXT captured statement. If it survived, it would
// leak onto an unrelated write later in the same request and the trail would
// claim the settings UPDATE was a "login".
func TestIntentIsConsumedOnce(t *testing.T) {
	ctx := WithIntent(context.Background(), Intent{Event: "login", Description: "Signed in"})

	first, ok := takeIntent(ctx)
	if !ok || first.Event != "login" || first.Description != "Signed in" {
		t.Fatalf("the first read should carry the intent, got %#v ok=%v", first, ok)
	}

	if got, ok := takeIntent(ctx); ok {
		t.Fatalf("the second read must not carry the same intent, got %#v", got)
	}
}

func TestTakeIntentOnAContextWithoutOne(t *testing.T) {
	if _, ok := takeIntent(context.Background()); ok {
		t.Fatal("a context with no intent must not answer ok=true")
	}
}

// One request's writes can be concurrent, and the intent cell is the only
// mutable state this package keeps in a context. Exactly one caller may win.
func TestIntentIsConsumedExactlyOnceUnderConcurrency(t *testing.T) {
	ctx := WithIntent(context.Background(), Intent{Event: "login"})

	const racers = 32
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func() {
			defer wg.Done()
			if _, ok := takeIntent(ctx); ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("esperava exatamente um vencedor, veio %d", wins)
	}
}

func TestScopeAndBatchRoundTrip(t *testing.T) {
	ctx := WithBatch(WithScope(context.Background(), "acme"), "req-1")

	if got := ScopeFromContext(ctx); got != "acme" {
		t.Fatalf("scope: %q", got)
	}
	if got := BatchFromContext(ctx); got != "req-1" {
		t.Fatalf("batch: %q", got)
	}
}

func TestScopeAndBatchDefaultToEmpty(t *testing.T) {
	if got := ScopeFromContext(context.Background()); got != "" {
		t.Fatalf("scope: esperava vazio, veio %q", got)
	}
	if got := BatchFromContext(context.Background()); got != "" {
		t.Fatalf("batch: esperava vazio, veio %q", got)
	}
}

// Intents are one-shot so they cannot leak onto an unrelated write later in
// the same request. A multi-statement act re-applies WithIntent per write.
func TestAnIntentIsOneShot(t *testing.T) {
	ctx := WithIntent(context.Background(), Intent{Event: "login"})

	if _, ok := takeIntent(ctx); !ok {
		t.Fatal("o primeiro take tinha de entregar o intent")
	}
	if _, ok := takeIntent(ctx); ok {
		t.Fatal("an intent must not survive the first statement")
	}
}
