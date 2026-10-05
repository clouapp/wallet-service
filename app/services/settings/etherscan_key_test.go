package settings

import (
	"context"
	"errors"
	"testing"
)

const (
	etherscanSettingsKey = "es-settings-key-9f3a"
	etherscanEnvKey      = "es-env-key-4c21"
)

func TestEtherscanKeyForHeight_EnabledGroupOpensTheSealedKey(t *testing.T) {
	store := newMemoryStore()
	store.PutPlatform(groupProviderEtherscan, map[string]string{
		keyProviderEnabled: "true",
		keyProviderAPIKey:  "enc:v1:" + etherscanSettingsKey,
	})

	got := newTestService(store).EtherscanKeyForHeight(context.Background(), etherscanEnvKey)
	if got != etherscanSettingsKey {
		t.Fatal("an enabled provider_etherscan row did not open its api key")
	}
	if got == etherscanEnvKey {
		t.Fatal("an enabled provider_etherscan row kept the environment key")
	}
}

func TestEtherscanKeyForHeight_MissingDisabledUnsealedAndFailedReadUseTheEnvKey(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		got := newTestService(newMemoryStore()).EtherscanKeyForHeight(context.Background(), "  "+etherscanEnvKey+"  ")
		if got != etherscanEnvKey {
			t.Fatal("a missing provider_etherscan row did not keep the environment key")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		store := newMemoryStore()
		store.PutPlatform(groupProviderEtherscan, map[string]string{
			keyProviderEnabled: "false",
			keyProviderAPIKey:  "enc:v1:" + etherscanSettingsKey,
		})
		got := newTestService(store).EtherscanKeyForHeight(context.Background(), etherscanEnvKey)
		if got != etherscanEnvKey {
			t.Fatal("a disabled provider_etherscan row replaced the environment key")
		}
	})

	t.Run("unsealed", func(t *testing.T) {
		store := newMemoryStore()
		store.PutPlatform(groupProviderEtherscan, map[string]string{
			keyProviderEnabled: "true",
			keyProviderAPIKey:  etherscanSettingsKey,
		})
		got := newTestService(store).EtherscanKeyForHeight(context.Background(), etherscanEnvKey)
		if got != etherscanEnvKey {
			t.Fatal("an unsealed provider_etherscan api key was used")
		}
	})

	t.Run("open failed", func(t *testing.T) {
		store := newMemoryStore()
		store.PutPlatform(groupProviderEtherscan, map[string]string{
			keyProviderEnabled: "true",
			keyProviderAPIKey:  "enc:v1:" + etherscanSettingsKey,
		})
		service := NewService(Deps{Store: store, Sealer: refuseEtherscanSealer{}, Cache: nopCache{}, Activity: discardActivity{}})
		got := service.EtherscanKeyForHeight(context.Background(), etherscanEnvKey)
		if got != etherscanEnvKey {
			t.Fatal("an api key whose seal did not open was used")
		}
	})

	t.Run("read failed", func(t *testing.T) {
		service := NewService(Deps{Store: platformErrStore{err: errors.New(etherscanSettingsKey)}, Sealer: prefixSealer{}, Cache: nopCache{}, Activity: discardActivity{}})
		got := service.EtherscanKeyForHeight(context.Background(), etherscanEnvKey)
		if got != etherscanEnvKey {
			t.Fatal("a failed provider_etherscan read did not keep the environment key")
		}
	})
}

func TestEtherscanKeyForHeight_NilServiceOrContextKeepsTheEnvKey(t *testing.T) {
	var service *Service
	if got := service.EtherscanKeyForHeight(context.Background(), etherscanEnvKey); got != etherscanEnvKey {
		t.Fatal("a nil settings service did not keep the environment key")
	}
	if got := newTestService(newMemoryStore()).EtherscanKeyForHeight(nil, etherscanEnvKey); got != etherscanEnvKey {
		t.Fatal("a nil context did not keep the environment key")
	}
}

type refuseEtherscanSealer struct{}

func (refuseEtherscanSealer) Seal(string) (string, error) {
	return "", errors.New("seal refused")
}

func (refuseEtherscanSealer) Open(string) (string, error) {
	return "", errors.New("open refused")
}
