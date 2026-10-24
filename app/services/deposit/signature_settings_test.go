package deposit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSignature_SettingsForRun_LaysTheStoredRowOverTheEnvironmentFieldByField(t *testing.T) {
	env := SignatureSyncSettings{Chains: []string{"sol"}, Interval: 600 * time.Second, Concurrency: 2, MaxPages: 10}
	cases := map[string]struct {
		chains                         []string
		chainsStored                   bool
		interval, concurrency, maxPage int
		readErr                        error
		want                           SignatureSyncSettings
	}{
		"no stored row keeps the environment": {
			want: env,
		},
		"every field stored": {
			chains: []string{"tsol", "sol"}, chainsStored: true, interval: 900, concurrency: 4, maxPage: 20,
			want: SignatureSyncSettings{Chains: []string{"tsol", "sol"}, Interval: 900 * time.Second, Concurrency: 4, MaxPages: 20},
		},
		"a stored empty chain list turns the sync off": {
			chainsStored: true,
			want:         SignatureSyncSettings{Interval: 600 * time.Second, Concurrency: 2, MaxPages: 10},
		},
		"one field stored": {
			interval: 60,
			want:     SignatureSyncSettings{Chains: []string{"sol"}, Interval: time.Minute, Concurrency: 2, MaxPages: 10},
		},
		"a failed read keeps the environment": {
			chains: []string{"tsol"}, chainsStored: true, interval: 60, readErr: errors.New("db down"),
			want: env,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := SignatureSettingsForRun(env, tc.chains, tc.chainsStored, tc.interval, tc.concurrency, tc.maxPage, tc.readErr)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSignature_SettingsForRun_BoundsAnEnvironmentOutOfRange(t *testing.T) {
	cases := map[string]struct {
		env  SignatureSyncSettings
		want SignatureSyncSettings
	}{
		"unset values are the defaults": {
			env:  SignatureSyncSettings{},
			want: DefaultSignatureSyncSettings(),
		},
		"an interval below 15 s is raised to 15 s": {
			env:  SignatureSyncSettings{Interval: 5 * time.Second, Concurrency: 2, MaxPages: 10},
			want: SignatureSyncSettings{Interval: MinSignatureSyncInterval, Concurrency: 2, MaxPages: 10},
		},
		"a concurrency or a page limit out of range is the default": {
			env:  SignatureSyncSettings{Interval: time.Minute, Concurrency: 64, MaxPages: -3},
			want: SignatureSyncSettings{Interval: time.Minute, Concurrency: DefaultSignatureSyncConcurrency, MaxPages: DefaultSignatureSyncMaxPages},
		},
		"chains are trimmed and listed once": {
			env:  SignatureSyncSettings{Chains: []string{" sol", "sol", "", "tsol "}, Interval: time.Minute, Concurrency: 1, MaxPages: 1},
			want: SignatureSyncSettings{Chains: []string{"sol", "tsol"}, Interval: time.Minute, Concurrency: 1, MaxPages: 1},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, SignatureSettingsForRun(tc.env, nil, false, 0, 0, 0, nil))
		})
	}
}

func TestSignature_Settings_AreTheDefaultsWithoutASourceAndNormalizedWithOne(t *testing.T) {
	assert.Equal(t, DefaultSignatureSyncSettings(), (&Service{}).SignatureSettings(context.Background()))
	assert.False(t, DefaultSignatureSyncSettings().Enabled("sol"), "nothing changes unless a chain is listed")

	svc := &Service{signatureSettings: func(context.Context) SignatureSyncSettings {
		return SignatureSyncSettings{Chains: []string{"sol "}, Interval: time.Second}
	}}
	got := svc.SignatureSettings(context.Background())
	assert.True(t, got.Enabled("sol"))
	assert.False(t, got.Enabled("tsol"))
	assert.Equal(t, MinSignatureSyncInterval, got.Interval)
}
