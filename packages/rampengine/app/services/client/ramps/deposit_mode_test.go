package ramps_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

// A deposit may only be simulated against a sandbox provider, so a mode this
// code does not know, or cannot read, is an error and never "simulated".
func TestDepositModeForProvider(t *testing.T) {
	cases := map[string]struct {
		mode    string
		readErr error
		want    string
		wantErr bool
	}{
		"sandbox":             {mode: models.ProviderModeSandbox, want: ramps.DepositModeSimulated},
		"production":          {mode: models.ProviderModeProduction, want: ramps.DepositModeOnChain},
		"unknown mode":        {mode: "staging", wantErr: true},
		"settings unreadable": {readErr: errors.New("settings: connection refused"), wantErr: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			settings := mocks.NewMockSettingsSource(t)
			group := ramp.ProviderGroup(models.ProviderBridge)
			settings.EXPECT().Bool(group, ramp.SettingEnabled).Return(true, c.readErr).Once()
			settings.EXPECT().String(group, ramp.SettingMode).Return(c.mode, nil).Maybe()

			mode, err := ramps.DepositModeForProvider(ramp.NewConfig(settings), models.ProviderBridge)
			require.Equal(t, c.wantErr, err != nil, "err = %v", err)
			require.Equal(t, c.want, mode)
		})
	}
}
