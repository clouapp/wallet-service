package providers

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/goravel/framework/contracts/config"
	"github.com/goravel/framework/contracts/foundation"
	contractslog "github.com/goravel/framework/contracts/log"
	frameworklog "github.com/goravel/framework/log"
	frameworklogger "github.com/goravel/framework/log/logger"

	"github.com/macrowallets/waas/app/services/security"
)

// InstallLogRedaction makes facades.Log() write through a redacting handler.
// It registers the standard channels when the process has none, then rewrites
// every file or stdout channel so its handler is security.NewRedactingHandler.
//
// Goravel caches a channel's handlers on first use (log/application.go
// getHandlers). This must run from the WithConfig hook, after config.Boot and
// before any service provider can log, or the unwrapped handler is pinned for
// the life of the process. The framework SQL logger interpolates bind
// arguments into the message before it reaches this sink, so redaction has to
// live here rather than at a call site.
func InstallLogRedaction(cfg config.Config, parser foundation.Json) []string {
	if cfg == nil {
		return nil
	}
	security.ConfigureRedaction(redactionMaterial(cfg))

	logging, _ := cfg.Get("logging").(map[string]any)
	if logging == nil {
		logging = map[string]any{}
	}
	channels, _ := logging["channels"].(map[string]any)
	if len(channels) == 0 {
		logging = defaultLogging(cfg, parser)
		channels, _ = logging["channels"].(map[string]any)
	}
	wrapped := redactLogChannels(channels, func(driver string) contractslog.Logger {
		switch driver {
		case contractslog.DriverSingle:
			return frameworklogger.NewSingle(cfg, parser)
		case contractslog.DriverDaily:
			return frameworklogger.NewDaily(cfg, parser)
		default:
			return nil
		}
	})
	logging["channels"] = channels
	if _, ok := logging["default"].(string); !ok || logging["default"] == "" {
		logging["default"] = defaultLogChannel(cfg)
	}
	cfg.Add("logging", logging)
	bindDefaultSlog(cfg)
	return wrapped
}

// bindDefaultSlog points slog.Default at the default channel's handler.
// redactLogChannels has already replaced that channel's driver with the
// redacting wrapper, so a slog record is handled by security.NewRedactingHandler
// — the same handler facades.Log() writes through — and not by a second sink.
func bindDefaultSlog(cfg config.Config) {
	logging, _ := cfg.Get("logging").(map[string]any)
	channels, _ := logging["channels"].(map[string]any)
	name, _ := logging["default"].(string)
	handlers := slogHandlers(name, channels, map[string]struct{}{})
	if len(handlers) == 0 {
		return
	}
	handler := handlers[0]
	if len(handlers) > 1 {
		handler = fanoutHandler{handlers: handlers}
	}
	slog.SetDefault(slog.New(handler))
}

// fanoutHandler writes one record to every handler of a stack channel.
type fanoutHandler struct {
	handlers []slog.Handler
}

func (f fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range f.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	var first error
	for _, handler := range f.handlers {
		if err := handler.Handle(ctx, record.Clone()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (f fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, handler := range f.handlers {
		next[i] = handler.WithAttrs(attrs)
	}
	return fanoutHandler{handlers: next}
}

func (f fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, handler := range f.handlers {
		next[i] = handler.WithGroup(name)
	}
	return fanoutHandler{handlers: next}
}

// slogHandlers resolves one channel to the slog handlers in front of its
// already-wrapped Goravel handler. A stack contributes each child. The
// Goravel handler is opened on the first record so installing the default
// logger does not open the log file by itself.
func slogHandlers(name string, channels map[string]any, seen map[string]struct{}) []slog.Handler {
	if name == "" {
		return nil
	}
	if _, loop := seen[name]; loop {
		return nil
	}
	seen[name] = struct{}{}
	channel, _ := channels[name].(map[string]any)
	if channel == nil {
		return nil
	}
	driver, _ := channel["driver"].(string)
	if driver == contractslog.DriverStack {
		var handlers []slog.Handler
		for _, child := range channelNames(channel["channels"]) {
			handlers = append(handlers, slogHandlers(child, channels, seen)...)
		}
		return handlers
	}
	via, ok := channel["via"].(contractslog.Logger)
	if !ok {
		return nil
	}
	return []slog.Handler{&channelSlogHandler{via: via, channel: "logging.channels." + name}}
}

func channelNames(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		names := make([]string, 0, len(typed))
		for _, item := range typed {
			name, ok := item.(string)
			if ok {
				names = append(names, name)
			}
		}
		return names
	default:
		return nil
	}
}

// channelSlogHandler is the default channel's redacting handler behind slog.
// via.Handle returns security.NewRedactingHandler; Goravel's adapter is what
// turns that handler into an slog.Handler.
type channelSlogHandler struct {
	via     contractslog.Logger
	channel string
	once    sync.Once
	inner   slog.Handler
}

func (h *channelSlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	inner := h.resolve()
	if inner == nil {
		return false
	}
	return inner.Enabled(ctx, level)
}

func (h *channelSlogHandler) Handle(ctx context.Context, record slog.Record) error {
	inner := h.resolve()
	if inner == nil {
		return nil
	}
	return inner.Handle(ctx, record)
}

func (h *channelSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	inner := h.resolve()
	if inner == nil {
		return h
	}
	return inner.WithAttrs(attrs)
}

func (h *channelSlogHandler) WithGroup(name string) slog.Handler {
	inner := h.resolve()
	if inner == nil {
		return h
	}
	return inner.WithGroup(name)
}

func (h *channelSlogHandler) resolve() slog.Handler {
	h.once.Do(func() {
		handler, err := h.via.Handle(h.channel)
		if err != nil || handler == nil {
			return
		}
		h.inner = frameworklog.HandlerToSlogHandler(handler)
	})
	return h.inner
}

func defaultLogging(cfg config.Config, parser foundation.Json) map[string]any {
	level := cfg.EnvString("LOG_LEVEL", "debug")
	return map[string]any{
		"default": defaultLogChannel(cfg),
		"channels": map[string]any{
			"stack": map[string]any{
				"driver":   contractslog.DriverStack,
				"channels": []string{"daily"},
			},
			"single": map[string]any{
				"driver":    contractslog.DriverSingle,
				"path":      "storage/logs/goravel.log",
				"level":     level,
				"print":     false,
				"formatter": frameworklogger.FormatterText,
			},
			"daily": map[string]any{
				"driver":    contractslog.DriverDaily,
				"path":      "storage/logs/goravel.log",
				"level":     level,
				"days":      7,
				"print":     false,
				"formatter": frameworklogger.FormatterText,
			},
			// stdout is custom so a Lambda process (read-only filesystem) still
			// has a sink. print on the file channels stays false: that console
			// mirror is a second handler the rewrite does not wrap.
			"stdout": map[string]any{
				"driver":    contractslog.DriverCustom,
				"level":     level,
				"formatter": frameworklogger.FormatterJson,
				"via":       stdoutLogger{config: cfg, parser: parser},
			},
		},
	}
}

func defaultLogChannel(cfg config.Config) string {
	if explicit := cfg.EnvString("LOG_CHANNEL", ""); explicit != "" {
		return explicit
	}
	if cfg.GetString("vault.lambda_mode") != "" {
		return "stdout"
	}
	return "stack"
}

// redactLogChannels rewrites, in place, every channel whose driver can be
// rebuilt. The driver becomes custom and via becomes a redacting wrapper. The
// rest of the channel (path, level, days, formatter) stays, because the
// wrapped logger reads those keys back off this same map.
func redactLogChannels(channels map[string]any, build func(driver string) contractslog.Logger) []string {
	var wrapped []string
	for name, raw := range channels {
		channel, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		driver, _ := channel["driver"].(string)
		inner := build(driver)
		if inner == nil {
			inner = existingCustomLogger(driver, channel)
		}
		if inner == nil {
			continue
		}
		channel["driver"] = contractslog.DriverCustom
		channel["via"] = redactingLogger{inner: inner}
		wrapped = append(wrapped, name)
	}
	sort.Strings(wrapped)
	return wrapped
}

func existingCustomLogger(driver string, channel map[string]any) contractslog.Logger {
	if driver != contractslog.DriverCustom {
		return nil
	}
	via, ok := channel["via"].(contractslog.Logger)
	if !ok {
		return nil
	}
	if _, already := via.(redactingLogger); already {
		return nil
	}
	return via
}

// redactingLogger defers to the framework logger and only wraps the handler
// it produces.
type redactingLogger struct {
	inner contractslog.Logger
}

func (l redactingLogger) Handle(channel string) (contractslog.Handler, error) {
	handler, err := l.inner.Handle(channel)
	if err != nil {
		return nil, err
	}
	return security.NewRedactingHandler(handler), nil
}

// stdoutLogger is the custom driver behind the stdout channel. It writes
// straight to os.Stdout; the single driver would join that path with the
// process-relative root and miss the device.
type stdoutLogger struct {
	config config.Config
	parser foundation.Json
}

func (l stdoutLogger) Handle(channel string) (contractslog.Handler, error) {
	level := frameworklogger.GetLevelFromString(l.config.GetString(channel + ".level"))
	formatter := l.config.GetString(channel+".formatter", frameworklogger.FormatterJson)
	return frameworklogger.NewIOHandler(os.Stdout, l.config, l.parser, level, formatter), nil
}

// redactionMaterial collects configured RPC hosts and secret values. Hosts
// come from RPC URLs; values come from secret-named config keys and from the
// environment variables that hold the same material (chain RPC URLs are often
// env:NAME references, not copied into the config map).
func redactionMaterial(cfg config.Config) (hosts, secrets []string) {
	for _, root := range []string{"vault", "database", "jwt", "mail", "auth"} {
		collectConfig(cfg.Get(root), "", &hosts, &secrets)
	}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !secretEnvKey(key) {
			continue
		}
		collectString(key, value, &hosts, &secrets)
	}
	return hosts, secrets
}

func collectConfig(value any, path string, hosts, secrets *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			next := key
			if path != "" {
				next = path + "." + key
			}
			collectConfig(child, next, hosts, secrets)
		}
	case string:
		collectString(path, typed, hosts, secrets)
	}
}

func collectString(path, value string, hosts, secrets *[]string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if rpcPath(path) {
		for _, piece := range strings.Split(value, ",") {
			host, parts := secretsInURL(strings.TrimSpace(piece))
			if host != "" {
				*hosts = append(*hosts, host)
			}
			*secrets = append(*secrets, parts...)
		}
	}
	if securityKey(path) {
		*secrets = append(*secrets, value)
	}
}

func rpcPath(path string) bool {
	return strings.Contains(strings.ToLower(path), "rpc")
}

func securityKey(path string) bool {
	k := strings.ToLower(path)
	switch {
	case strings.Contains(k, "password"),
		strings.Contains(k, "passphrase"),
		strings.Contains(k, "secret"),
		strings.Contains(k, "api_key"),
		strings.Contains(k, "apikey"),
		strings.Contains(k, "token"),
		strings.Contains(k, "private"):
		return true
	default:
		return false
	}
}

func secretEnvKey(key string) bool {
	k := strings.ToUpper(key)
	switch {
	case strings.Contains(k, "RPC_URL"),
		strings.Contains(k, "API_KEY"),
		strings.Contains(k, "SECRET"),
		strings.Contains(k, "PASSPHRASE"),
		strings.Contains(k, "PASSWORD"),
		strings.Contains(k, "PRIVATE"),
		strings.Contains(k, "WEBHOOK"):
		return true
	default:
		return false
	}
}

// secretsInURL returns the hostname and the credential pieces of one URL.
// A URL that does not carry a credential still returns its host when the
// caller asked about an RPC path, so a later line that adds the key in the
// path is masked. Pieces shorter than the segment minimum are left out.
func secretsInURL(raw string) (string, []string) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return "", nil
	}
	var parts []string
	if u.User != nil {
		if password, ok := u.User.Password(); ok {
			parts = append(parts, password)
		}
		if name := u.User.Username(); name != "" {
			parts = append(parts, name)
		}
	}
	for _, segment := range strings.Split(u.EscapedPath(), "/") {
		if len(segment) >= 16 {
			parts = append(parts, segment)
		}
	}
	query, _ := url.ParseQuery(u.RawQuery)
	for _, values := range query {
		for _, value := range values {
			if len(value) >= 8 {
				parts = append(parts, value)
			}
		}
	}
	return u.Hostname(), parts
}
