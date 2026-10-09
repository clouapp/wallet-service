package ingest

import (
	"context"
	"errors"
	"testing"

	"github.com/macrowallets/waas/app/services/ingest/providers"
)

type recordedIngest struct {
	calls int
	event InboundEvent
	err   error
}

func (r *recordedIngest) Ingest(_ context.Context, event InboundEvent) error {
	r.calls++
	r.event = event
	return r.err
}

type parsingProvider struct {
	providers.WebhookProvider
	transfers []providers.InboundTransfer
	err       error
	got       []byte
}

func (p *parsingProvider) ParsePayload(body []byte) ([]providers.InboundTransfer, error) {
	p.got = body
	return p.transfers, p.err
}

func lookupOf(provider providers.WebhookProvider) func() map[string]providers.WebhookProvider {
	return func() map[string]providers.WebhookProvider {
		return map[string]providers.WebhookProvider{"alchemy": provider}
	}
}

func TestInbound_Receive_HandsATypedEventToTheService(t *testing.T) {
	provider := &parsingProvider{transfers: []providers.InboundTransfer{{TxHash: "tx-typed"}}}
	sink := &recordedIngest{}

	err := NewInbound(sink, lookupOf(provider)).Receive(context.Background(), Webhook{Provider: "alchemy", ChainID: "eth", Body: []byte("raw")})
	if err != nil {
		t.Fatal(err)
	}
	if string(provider.got) != "raw" || sink.calls != 1 || sink.event.ChainID != "eth" || len(sink.event.Transfers) != 1 || sink.event.Transfers[0].TxHash != "tx-typed" {
		t.Fatalf("parsed %q event %+v calls %d", provider.got, sink.event, sink.calls)
	}
}

func TestInbound_Receive_RefusesAnUnknownProviderWithoutIngesting(t *testing.T) {
	sink := &recordedIngest{}

	err := NewInbound(sink, lookupOf(&parsingProvider{})).Receive(context.Background(), Webhook{Provider: "nobody", ChainID: "eth"})
	if !errors.Is(err, ErrUnknownProvider) || sink.calls != 0 {
		t.Fatalf("err = %v calls %d", err, sink.calls)
	}
}

func TestInbound_Receive_WithoutALookupKnowsOnlyTheRegisteredAdapters(t *testing.T) {
	sink := &recordedIngest{}

	err := NewInbound(sink, nil).Receive(context.Background(), Webhook{Provider: "nobody", ChainID: "eth"})
	if !errors.Is(err, ErrUnknownProvider) || sink.calls != 0 {
		t.Fatalf("err = %v calls %d", err, sink.calls)
	}
}

func TestInbound_Receive_DropsTheParseErrorAndDoesNotIngest(t *testing.T) {
	provider := &parsingProvider{err: errors.New(`unexpected token in {"marker":"RAW-WEBHOOK-BODY"}`)}
	sink := &recordedIngest{}

	err := NewInbound(sink, lookupOf(provider)).Receive(context.Background(), Webhook{Provider: "alchemy", ChainID: "eth", Body: []byte("raw")})
	if !errors.Is(err, ErrInvalidPayload) || sink.calls != 0 {
		t.Fatalf("err = %v calls %d", err, sink.calls)
	}
	if err.Error() != "invalid payload" {
		t.Fatalf("the parse error leaked: %v", err)
	}
}

func TestInbound_Receive_ReturnsTheServiceFailureUnchanged(t *testing.T) {
	down := errors.New("db down")
	sink := &recordedIngest{err: down}

	err := NewInbound(sink, lookupOf(&parsingProvider{})).Receive(context.Background(), Webhook{Provider: "alchemy", ChainID: "eth"})
	if !errors.Is(err, down) {
		t.Fatalf("err = %v", err)
	}
}
