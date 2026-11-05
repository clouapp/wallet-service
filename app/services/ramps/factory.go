package ramps

import (
	"context"
	"fmt"
	"strconv"

	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/bridge"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/mutualpay"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/ozav"
)

type SettingsReader struct {
	Service        *settings.Service
	Mode           string
	AppEnvironment string
	BaseURLs       map[string]string
}

func (r SettingsReader) String(group, key string) (string, error) {
	return r.Service.Raw(context.Background(), nil, settings.RampGroupName(group, r.Mode), key)
}
func (r SettingsReader) Bool(group, key string) (bool, error) {
	return r.Service.Bool(context.Background(), nil, settings.RampGroupName(group, r.Mode), key)
}
func (r SettingsReader) Secret(group, key string) (string, error) {
	return r.Service.Secret(context.Background(), nil, settings.RampGroupName(group, r.Mode), key)
}

func providerKey(reader SettingsReader, code string) (string, error) {
	mode, err := reader.String(ramp.ProviderGroup(code), ramp.SettingMode)
	if err != nil || mode != reader.Mode {
		return "", fmt.Errorf("ramp provider environment mismatch")
	}
	key, err := reader.Secret(ramp.ProviderGroup(code), ramp.SettingAPIKey)
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", ramp.ErrNoProviderAvailable
	}
	return key, nil
}

func buildFactory(reader SettingsReader) *ramp.Factory {
	secret := func(code, key string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return reader.Secret(ramp.ProviderGroup(code), key) }
	}
	b := bridge.New(func(context.Context) (*bridge.Client, error) {
		key, e := providerKey(reader, ramp.ProviderBridge)
		if e != nil {
			return nil, e
		}
		base, e := bridge.BaseURLForMode(reader.Mode)
		if e != nil {
			return nil, e
		}
		return bridge.NewClient(bridge.Config{APIKey: key, BaseURL: reader.baseURL("bridge", base)}), nil
	}, secret(ramp.ProviderBridge, ramp.SettingWebhookPublicKey))
	o := ozav.New(func(context.Context) (*ozav.Client, error) {
		key, e := providerKey(reader, ramp.ProviderOzav)
		if e != nil {
			return nil, e
		}
		if e = ozav.ValidateKeyMode(key, reader.Mode); e != nil {
			return nil, e
		}
		return ozav.NewClient(ozav.Config{APIKey: key, BaseURL: reader.baseURL("ozav", ozav.BaseURL)}), nil
	}, secret(ramp.ProviderOzav, ramp.SettingWebhookSecret))
	bp := blindpay.New(func(context.Context) (*blindpay.Client, error) {
		key, e := providerKey(reader, ramp.ProviderBlindPay)
		if e != nil {
			return nil, e
		}
		g := ramp.ProviderGroup(ramp.ProviderBlindPay)
		instance, e := reader.String(g, ramp.SettingInstanceID)
		if e != nil {
			return nil, e
		}
		delegate, e := reader.String(g, ramp.SettingSolanaDelegateAddress)
		if e != nil {
			return nil, e
		}
		rpc, e := reader.Secret(g, ramp.SettingSolanaRPCURL)
		if e != nil {
			return nil, e
		}
		return blindpay.NewClient(blindpay.Config{APIKey: key, InstanceID: instance, Mode: reader.Mode, Delegate: delegate, SolanaRPCURL: rpc, Mints: blindpayMints(reader), BaseURL: reader.baseURL("blindpay", blindpay.BaseURL)}), nil
	}, secret(ramp.ProviderBlindPay, ramp.SettingWebhookSecret), func(token string) bool {
		if _, err := providerKey(reader, ramp.ProviderBlindPay); err != nil {
			return false
		}
		if _, ok := blindpayMints(reader)[token]; !ok {
			return false
		}
		g := ramp.ProviderGroup(ramp.ProviderBlindPay)
		rpc, e := reader.Secret(g, ramp.SettingSolanaRPCURL)
		if e != nil || rpc == "" {
			return false
		}
		delegate, e := reader.String(g, ramp.SettingSolanaDelegateAddress)
		if e != nil || delegate == "" {
			return false
		}
		if reader.Mode == "production" {
			v, e := reader.Bool(g, ramp.SettingProductionPayoutConfirmation)
			return e == nil && v
		}
		return true
	})
	return ramp.NewFactory(b, o, bp, mutualpay.New())
}

func blindpayMints(reader SettingsReader) map[string]blindpay.MintConfig {
	out := map[string]blindpay.MintConfig{}
	g := ramp.ProviderGroup(ramp.ProviderBlindPay)
	for _, t := range []struct{ token, mint, decimals string }{{"USDC", ramp.SettingSolanaUSDCMint, ramp.SettingSolanaUSDCDecimals}, {"USDT", ramp.SettingSolanaUSDTMint, ramp.SettingSolanaUSDTDecimals}} {
		mint, e := reader.String(g, t.mint)
		if e != nil || mint == "" {
			continue
		}
		raw, e := reader.String(g, t.decimals)
		if e != nil {
			continue
		}
		dec, e := strconv.Atoi(raw)
		if e != nil || dec < 0 || dec > 18 {
			continue
		}
		out[t.token] = blindpay.MintConfig{Address: mint, Decimals: uint8(dec)}
	}
	return out
}

func (reader SettingsReader) baseURL(code, fallback string) string {
	if reader.AppEnvironment != "testing" && reader.AppEnvironment != "local" {
		return fallback
	}
	if value := reader.BaseURLs[code]; value != "" {
		return value
	}
	return fallback
}
