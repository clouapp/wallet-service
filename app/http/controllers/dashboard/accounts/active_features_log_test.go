package accounts

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestLogActiveFeaturesFailureOmitsSessionJWT(t *testing.T) {
	const fixture = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.fixture-signature"
	ctx := context.WithValue(context.Background(), struct{ name string }{"session"}, fixture)

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	logActiveFeaturesFailure(ctx, errors.New("load features"))
	written := logs.String()
	if strings.Contains(written, fixture) {
		t.Fatal("active features log wrote the session JWT")
	}
	if !strings.Contains(written, "account: active features") {
		t.Fatal("expected an error log that active features could not be read")
	}
}
