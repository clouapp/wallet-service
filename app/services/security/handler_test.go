package security

import (
	"context"
	"strings"
	"testing"
	"time"

	contractslog "github.com/goravel/framework/contracts/log"
)

type recordingHandler struct {
	message string
	with    map[string]any
}

func (h *recordingHandler) Enabled(contractslog.Level) bool { return true }

func (h *recordingHandler) Handle(entry contractslog.Entry) error {
	h.message = entry.Message()
	h.with = entry.With()
	return nil
}

type handlerEntry struct {
	message string
	with    map[string]any
}

func (e handlerEntry) Code() string              { return "" }
func (e handlerEntry) Context() context.Context  { return context.Background() }
func (e handlerEntry) Data() contractslog.Data   { return nil }
func (e handlerEntry) Domain() string            { return "" }
func (e handlerEntry) Hint() string              { return "" }
func (e handlerEntry) Level() contractslog.Level { return contractslog.LevelError }
func (e handlerEntry) Message() string           { return e.message }
func (e handlerEntry) Owner() any                { return nil }
func (e handlerEntry) Request() map[string]any   { return nil }
func (e handlerEntry) Response() map[string]any  { return nil }
func (e handlerEntry) Tags() []string            { return nil }
func (e handlerEntry) Time() time.Time           { return time.Time{} }
func (e handlerEntry) Trace() map[string]any     { return nil }
func (e handlerEntry) User() any                 { return nil }
func (e handlerEntry) With() map[string]any      { return e.with }

func TestRedactingHandlerHidesSecretsBeforeTheInnerHandler(t *testing.T) {
	ConfigureRedaction([]string{"rpc.example"}, []string{"fake-api-key-value"})
	t.Cleanup(func() { ConfigureRedaction(nil, nil) })

	inner := &recordingHandler{}
	handler := NewRedactingHandler(inner)
	err := handler.Handle(handlerEntry{
		message: "sweep https://rpc.example/v2/fake-path-key-value?apikey=fake-query-key failed, key fake-api-key-value",
		with: map[string]any{
			"passphrase":     "fake-passphrase-value",
			"private_key":    "fake-private-key-material",
			"totp":           "fake-totp-secret",
			"share_a":        "fake-share-material",
			"webhook_secret": "fake-webhook-secret",
			"wallet_id":      "9f0e3c1a",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		"fake-path-key-value",
		"fake-query-key",
		"fake-api-key-value",
		"fake-passphrase-value",
		"fake-private-key-material",
		"fake-totp-secret",
		"fake-share-material",
		"fake-webhook-secret",
	} {
		if strings.Contains(inner.message, secret) {
			t.Fatalf("message leaked %q: %q", secret, inner.message)
		}
		for _, value := range inner.with {
			if text, ok := value.(string); ok && strings.Contains(text, secret) {
				t.Fatalf("field leaked %q: %#v", secret, inner.with)
			}
		}
	}
	if inner.with["wallet_id"] != "9f0e3c1a" {
		t.Fatalf("identifier was redacted: %#v", inner.with)
	}
	if inner.with["passphrase"] != "[redacted]" || inner.with["private_key"] != "[redacted]" {
		t.Fatalf("secret fields were not replaced: %#v", inner.with)
	}
}
