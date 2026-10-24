package deposit

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"
)

const (
	// DefaultSignatureSyncInterval is how often the signature sync reads a chain.
	DefaultSignatureSyncInterval = 10 * time.Minute
	// MinSignatureSyncInterval is the shortest interval the sync accepts: the local
	// workers' signature loop ticks every 15 s.
	MinSignatureSyncInterval = 15 * time.Second
	// DefaultSignatureSyncConcurrency is how many accounts are read at once: public
	// endpoints limit each method per IP (devnet: 10 getTransaction per 10 s).
	DefaultSignatureSyncConcurrency = 2
	// MaxSignatureSyncConcurrency bounds the accounts read at once.
	MaxSignatureSyncConcurrency = 16
	// DefaultSignatureSyncMaxPages bounds, in pages of 1000 signatures, the history
	// of an account read without a floor (a chain without a scan checkpoint).
	DefaultSignatureSyncMaxPages = 10
	// MaxSignatureSyncMaxPages bounds that setting.
	MaxSignatureSyncMaxPages = 1000
)

// SignatureSyncSettings are the signature sync's knobs, read at the moment of use
// (the deposit_signatures platform setting over the DEPOSIT_SIGNATURE_* environment).
// Chains are the chains whose deposits the sync reads and whose block scan pauses;
// Interval is how often the local workers sync one of them; Concurrency is how many
// accounts are read at once; MaxPages bounds the history read without a floor.
type SignatureSyncSettings struct {
	Chains      []string
	Interval    time.Duration
	Concurrency int
	MaxPages    int
}

// DefaultSignatureSyncSettings syncs no chain, every 10 minutes, 2 accounts at a
// time, at most 10 pages of history without a floor.
func DefaultSignatureSyncSettings() SignatureSyncSettings {
	return SignatureSyncSettings{
		Interval:    DefaultSignatureSyncInterval,
		Concurrency: DefaultSignatureSyncConcurrency,
		MaxPages:    DefaultSignatureSyncMaxPages,
	}
}

// Enabled reports whether chainID is one of the chains the signature sync reads.
func (s SignatureSyncSettings) Enabled(chainID string) bool {
	return slices.Contains(s.Chains, chainID)
}

// normalized trims and deduplicates the chains and replaces an interval below the
// minimum with the minimum, and a concurrency or a page limit out of range with its
// default, each logged.
func (s SignatureSyncSettings) normalized() SignatureSyncSettings {
	out := SignatureSyncSettings{Interval: s.Interval, Concurrency: s.Concurrency, MaxPages: s.MaxPages}
	for _, chainID := range s.Chains {
		chainID = strings.TrimSpace(chainID)
		if chainID != "" && !slices.Contains(out.Chains, chainID) {
			out.Chains = append(out.Chains, chainID)
		}
	}
	if out.Interval < MinSignatureSyncInterval {
		if out.Interval != 0 {
			slog.Warn("deposit signature sync interval below the minimum; the minimum is used", "interval", out.Interval.String(), "minimum", MinSignatureSyncInterval.String())
			out.Interval = MinSignatureSyncInterval
		} else {
			out.Interval = DefaultSignatureSyncInterval
		}
	}
	if out.Concurrency < 1 || out.Concurrency > MaxSignatureSyncConcurrency {
		if out.Concurrency != 0 {
			slog.Warn("deposit signature sync concurrency out of range; the default is used", "concurrency", out.Concurrency, "maximum", MaxSignatureSyncConcurrency)
		}
		out.Concurrency = DefaultSignatureSyncConcurrency
	}
	if out.MaxPages < 1 || out.MaxPages > MaxSignatureSyncMaxPages {
		if out.MaxPages != 0 {
			slog.Warn("deposit signature sync page limit out of range; the default is used", "max_pages", out.MaxPages, "maximum", MaxSignatureSyncMaxPages)
		}
		out.MaxPages = DefaultSignatureSyncMaxPages
	}
	return out
}

// SignatureSettingsForRun lays a stored deposit_signatures row over the environment
// values env for one use. A stored chain list (chainsStored, even an empty one, which
// turns the sync off) replaces the environment's; a positive stored interval
// (seconds), concurrency or page limit replaces the environment's, and zero (a missing
// or invalid stored field) keeps it. A failed read (readErr) keeps the environment
// values, so a settings outage never stops a chain's deposit detection.
func SignatureSettingsForRun(env SignatureSyncSettings, storedChains []string, chainsStored bool, storedIntervalSeconds, storedConcurrency, storedMaxPages int, readErr error) SignatureSyncSettings {
	if readErr != nil {
		slog.Warn("deposit signature settings unread; keeping the environment values", "error", readErr)
		return env.normalized()
	}
	settings := env
	if chainsStored {
		settings.Chains = append([]string(nil), storedChains...)
	}
	if storedIntervalSeconds > 0 {
		settings.Interval = time.Duration(storedIntervalSeconds) * time.Second
	}
	if storedConcurrency > 0 {
		settings.Concurrency = storedConcurrency
	}
	if storedMaxPages > 0 {
		settings.MaxPages = storedMaxPages
	}
	return settings.normalized()
}

// signatureSettingsSource reads the signature sync's settings at the moment of use.
type signatureSettingsSource func(ctx context.Context) SignatureSyncSettings

// SignatureSettings returns the signature sync's settings now: the configured
// source's (normalized), or the defaults without one.
func (s *Service) SignatureSettings(ctx context.Context) SignatureSyncSettings {
	if s == nil || s.signatureSettings == nil {
		return DefaultSignatureSyncSettings()
	}
	return s.signatureSettings(ctx).normalized()
}
