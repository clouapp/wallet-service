package ramps

import (
	"context"
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	er "github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

const ProviderTimelineRetentionDays = 90

func (s *Service) PruneTimeline(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		return 0, fmt.Errorf("timeline retention days must be positive")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return er.NewProviderCallRepository().DeleteOlderThan(days)
}

// CheckBlindPay makes the source integration's authenticated instance check,
// without creating a customer, submitting compliance or transferring funds.
func (s *Service) CheckBlindPay(ctx context.Context, mode string) (string, error) {
	if mode != "sandbox" && mode != "production" {
		return "", fmt.Errorf("invalid ramp environment")
	}
	reader := SettingsReader{Service: s.d.Settings, Mode: mode, AppEnvironment: s.d.AppEnvironment, BaseURLs: s.d.ProviderBaseURLs}
	adapter, err := s.d.Factory(reader).Get(ramp.ProviderBlindPay)
	if err != nil {
		return "", err
	}
	checker, ok := adapter.(*blindpay.Adapter)
	if !ok {
		return "", fmt.Errorf("configured provider does not support the BlindPay instance check")
	}
	call, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := checker.Check(call)
	return result.InstanceID, err
}
