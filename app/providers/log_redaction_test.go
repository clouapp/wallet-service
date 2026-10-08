package providers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/goravel/framework/contracts/config"
	contractslog "github.com/goravel/framework/contracts/log"

	"github.com/macrowallets/waas/app/services/security"
)

func TestInstall_Log_RedactionWrapsFileAndStdoutChannels(t *testing.T) {
	preserveDefaultSlog(t)
	cfg := &mapConfig{data: map[string]any{
		"vault": map[string]any{
			"rpc": map[string]any{
				"eth": "https://user:fake-rpc-password@rpc.example/v2/fake-path-key-value?apikey=fake-query-key",
			},
			"api_key_secret": "fake-api-key-value",
		},
	}}

	wrapped := InstallLogRedaction(cfg, nil)
	if strings.Join(wrapped, ",") != "daily,single,stdout" {
		t.Fatalf("wrapped channels = %v", wrapped)
	}

	logging := cfg.Get("logging").(map[string]any)
	if logging["default"] != "stack" {
		t.Fatalf("default channel = %#v, want stack", logging["default"])
	}
	channels := logging["channels"].(map[string]any)
	if channels["stack"].(map[string]any)["driver"] != contractslog.DriverStack {
		t.Fatal("stack channel must stay a stack")
	}
	daily := channels["daily"].(map[string]any)
	if daily["driver"] != contractslog.DriverCustom {
		t.Fatalf("daily driver = %#v", daily["driver"])
	}
	if daily["path"] != "storage/logs/goravel.log" {
		t.Fatalf("daily path was clobbered: %#v", daily)
	}
	if _, ok := daily["via"].(contractslog.Logger); !ok {
		t.Fatalf("daily via is not a logger: %#v", daily["via"])
	}

	got := security.RedactText("using fake-api-key-value and fake-path-key-value and fake-rpc-password and fake-query-key")
	for _, secret := range []string{"fake-api-key-value", "fake-path-key-value", "fake-rpc-password", "fake-query-key"} {
		if strings.Contains(got, secret) {
			t.Fatalf("install left %q in %q", secret, got)
		}
	}

	if again := InstallLogRedaction(cfg, nil); len(again) != 0 {
		t.Fatalf("second install wrapped %v", again)
	}
}

func TestInstall_Log_RedactionUsesStdoutWhenLambdaModeIsSet(t *testing.T) {
	preserveDefaultSlog(t)
	cfg := &mapConfig{data: map[string]any{
		"vault": map[string]any{"lambda_mode": "api"},
	}}
	InstallLogRedaction(cfg, nil)
	logging := cfg.Get("logging").(map[string]any)
	if logging["default"] != "stdout" {
		t.Fatalf("default channel = %#v, want stdout", logging["default"])
	}
}

func TestInstall_Log_RedactionRoutesSlogThroughTheSameHandler(t *testing.T) {
	preserveDefaultSlog(t)
	t.Cleanup(func() { security.ConfigureRedaction(nil, nil) })

	inner := &stubHandler{}
	cfg := &mapConfig{data: map[string]any{
		"logging": map[string]any{
			"default": "app",
			"channels": map[string]any{
				"app": map[string]any{
					"driver": contractslog.DriverCustom,
					"via":    stubLogger{handler: inner},
				},
			},
		},
		"vault": map[string]any{
			"rpc": map[string]any{
				"eth": "https://user:fake-rpc-password@rpc.example/v2/fake-path-key-value?apikey=fake-query-key",
			},
			"api_key_secret": "fake-api-key-value",
		},
	}}

	InstallLogRedaction(cfg, nil)
	slog.Info(
		"dial https://user:fake-rpc-password@rpc.example/v2/fake-path-key-value?apikey=fake-query-key",
		"url", "https://user:fake-rpc-password@rpc.example/v2/fake-path-key-value?apikey=fake-query-key",
		"api_key", "fake-api-key-value",
		"wallet", "9f0e3c1a",
	)

	if len(inner.got) != 1 {
		t.Fatalf("slog records = %#v", inner.got)
	}
	logged := inner.got[0] + " " + fmt.Sprint(inner.with)
	for _, secret := range []string{
		"fake-rpc-password",
		"fake-path-key-value",
		"fake-query-key",
		"fake-api-key-value",
	} {
		if strings.Contains(logged, secret) {
			t.Fatalf("slog leaked %q: %s", secret, logged)
		}
	}
	if !strings.Contains(inner.got[0], "dial") {
		t.Fatalf("message was dropped: %s", inner.got[0])
	}
	if len(inner.with) != 1 || inner.with[0]["api_key"] != "[redacted]" || inner.with[0]["wallet"] != "9f0e3c1a" {
		t.Fatalf("fields = %#v", inner.with)
	}
}

func TestRedact_Log_ChannelsRedactsBeforeTheFrameworkHandler(t *testing.T) {
	security.ConfigureRedaction([]string{"rpc.example"}, nil)
	t.Cleanup(func() { security.ConfigureRedaction(nil, nil) })

	inner := &stubHandler{}
	channels := map[string]any{
		"daily": map[string]any{"driver": contractslog.DriverDaily, "path": "storage/logs/goravel.log"},
	}
	redactLogChannels(channels, func(driver string) contractslog.Logger {
		if driver == contractslog.DriverDaily {
			return stubLogger{handler: inner}
		}
		return nil
	})
	via := channels["daily"].(map[string]any)["via"].(contractslog.Logger)
	handler, err := via.Handle("logging.channels.daily")
	if err != nil {
		t.Fatal(err)
	}
	message := `INSERT INTO wallets (note) VALUES ('https://rpc.example/v2/fake-path-key-value?apikey=fake-query-key')`
	if err := handler.Handle(stubEntry{message: message}); err != nil {
		t.Fatal(err)
	}
	if len(inner.got) != 1 {
		t.Fatalf("entry did not reach the handler: %#v", inner.got)
	}
	if strings.Contains(inner.got[0], "fake-path-key-value") || strings.Contains(inner.got[0], "fake-query-key") {
		t.Fatalf("statement reached the handler unredacted: %s", inner.got[0])
	}
	if !strings.Contains(inner.got[0], "INSERT INTO wallets") {
		t.Fatalf("redaction destroyed the statement: %s", inner.got[0])
	}
}

type stubLogger struct{ handler *stubHandler }

func (l stubLogger) Handle(string) (contractslog.Handler, error) { return l.handler, nil }

type stubHandler struct {
	got  []string
	with []map[string]any
}

func (h *stubHandler) Enabled(contractslog.Level) bool { return true }

func (h *stubHandler) Handle(entry contractslog.Entry) error {
	h.got = append(h.got, entry.Message())
	if fields := entry.With(); len(fields) > 0 {
		h.with = append(h.with, fields)
	}
	return nil
}

func preserveDefaultSlog(t *testing.T) {
	t.Helper()
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
}

type stubEntry struct{ message string }

func (e stubEntry) Code() string              { return "" }
func (e stubEntry) Context() context.Context  { return context.Background() }
func (e stubEntry) Data() contractslog.Data   { return nil }
func (e stubEntry) Domain() string            { return "" }
func (e stubEntry) Hint() string              { return "" }
func (e stubEntry) Level() contractslog.Level { return contractslog.LevelInfo }
func (e stubEntry) Message() string           { return e.message }
func (e stubEntry) Owner() any                { return nil }
func (e stubEntry) Request() map[string]any   { return nil }
func (e stubEntry) Response() map[string]any  { return nil }
func (e stubEntry) Tags() []string            { return nil }
func (e stubEntry) Time() time.Time           { return time.Time{} }
func (e stubEntry) Trace() map[string]any     { return nil }
func (e stubEntry) User() any                 { return nil }
func (e stubEntry) With() map[string]any      { return nil }

type mapConfig struct {
	data map[string]any
	env  map[string]string
}

func (m *mapConfig) Add(name string, configuration any) {
	if m.data == nil {
		m.data = map[string]any{}
	}
	m.data[name] = configuration
}

func (m *mapConfig) Get(path string, defaultValue ...any) any {
	cur := any(m.data)
	for _, part := range strings.Split(path, ".") {
		asMap, ok := cur.(map[string]any)
		if !ok {
			return first(defaultValue)
		}
		next, ok := asMap[part]
		if !ok {
			return first(defaultValue)
		}
		cur = next
	}
	return cur
}

func (m *mapConfig) GetString(path string, defaultValue ...string) string {
	value := m.Get(path)
	text, ok := value.(string)
	if !ok || text == "" {
		if len(defaultValue) > 0 {
			return defaultValue[0]
		}
		return ""
	}
	return text
}

func (m *mapConfig) GetInt(string, ...int) int                          { return 0 }
func (m *mapConfig) GetBool(string, ...bool) bool                       { return false }
func (m *mapConfig) GetDuration(string, ...time.Duration) time.Duration { return 0 }
func (m *mapConfig) UnmarshalKey(string, any) error                     { return nil }
func (m *mapConfig) EnvBool(string, ...bool) bool                       { return false }

func (m *mapConfig) Env(key string, defaultValue ...any) any {
	if value, ok := m.env[key]; ok {
		return value
	}
	return first(defaultValue)
}

func (m *mapConfig) EnvString(key string, defaultValue ...string) string {
	if value, ok := m.env[key]; ok && value != "" {
		return value
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return ""
}

func first(values []any) any {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

var _ config.Config = (*mapConfig)(nil)
