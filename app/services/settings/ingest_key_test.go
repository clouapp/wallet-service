package settings

import (
	"context"
	"errors"
	"testing"
)

const (
	ingestOpenedKey = "ing-opened-a91c"
	ingestEnvKey    = "ing-env-44d0"
)

func TestIngestProviderKey_EnabledGroupOpensTheSealedCredential(t *testing.T) {
	store := newMemoryStore()
	store.PutPlatform(groupProviderAlchemy, map[string]string{
		keyProviderEnabled:   "true",
		keyProviderAuthToken: "enc:v1:" + ingestOpenedKey,
	})

	got := newTestService(store).IngestProviderKey(context.Background(), ingestProviderAlchemy, ingestEnvKey)
	if got != ingestOpenedKey {
		t.Fatal("an enabled provider_alchemy row did not open its auth token")
	}
}

func TestIngestProviderKey_HeliusAndQuickNodeOpenAPIKey(t *testing.T) {
	for _, provider := range []string{ingestProviderHelius, ingestProviderQuickNode} {
		group, secretKey, ok := ingestProviderGroup(provider)
		if !ok {
			t.Fatal("ingest provider group was not mapped")
		}
		store := newMemoryStore()
		store.PutPlatform(group, map[string]string{
			keyProviderEnabled: "true",
			secretKey:          "enc:v1:" + ingestOpenedKey,
		})
		got := newTestService(store).IngestProviderKey(context.Background(), provider, ingestEnvKey)
		if got != ingestOpenedKey {
			t.Fatal("an enabled ingest provider row did not open its api key")
		}
	}
}

func TestIngestProviderKey_MissingDisabledUnsealedAndFailedReadUseTheEnvKey(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		got := newTestService(newMemoryStore()).IngestProviderKey(context.Background(), ingestProviderAlchemy, "  "+ingestEnvKey+"  ")
		if got != ingestEnvKey {
			t.Fatal("a missing provider_alchemy row did not keep the environment key")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		store := newMemoryStore()
		store.PutPlatform(groupProviderAlchemy, map[string]string{
			keyProviderEnabled:   "false",
			keyProviderAuthToken: "enc:v1:" + ingestOpenedKey,
		})
		got := newTestService(store).IngestProviderKey(context.Background(), ingestProviderAlchemy, ingestEnvKey)
		if got != ingestEnvKey {
			t.Fatal("a disabled provider_alchemy row replaced the environment key")
		}
	})

	t.Run("unsealed", func(t *testing.T) {
		store := newMemoryStore()
		store.PutPlatform(groupProviderHelius, map[string]string{
			keyProviderEnabled: "true",
			keyProviderAPIKey:  ingestOpenedKey,
		})
		got := newTestService(store).IngestProviderKey(context.Background(), ingestProviderHelius, ingestEnvKey)
		if got != ingestEnvKey {
			t.Fatal("an unsealed provider_helius api key was used")
		}
	})

	t.Run("open failed", func(t *testing.T) {
		store := newMemoryStore()
		store.PutPlatform(groupProviderQuickNode, map[string]string{
			keyProviderEnabled: "true",
			keyProviderAPIKey:  "enc:v1:" + ingestOpenedKey,
		})
		service := NewService(Deps{Store: store, Sealer: refuseIngestSealer{}, Cache: nopCache{}, Activity: discardActivity{}})
		got := service.IngestProviderKey(context.Background(), ingestProviderQuickNode, ingestEnvKey)
		if got != ingestEnvKey {
			t.Fatal("an api key whose seal did not open was used")
		}
	})

	t.Run("read failed", func(t *testing.T) {
		service := NewService(Deps{Store: platformErrStore{err: errors.New(ingestOpenedKey)}, Sealer: prefixSealer{}, Cache: nopCache{}, Activity: discardActivity{}})
		got := service.IngestProviderKey(context.Background(), ingestProviderAlchemy, ingestEnvKey)
		if got != ingestEnvKey {
			t.Fatal("a failed provider_alchemy read did not keep the environment key")
		}
	})
}

func TestIngestProviderKey_NilServiceOrContextKeepsTheEnvKey(t *testing.T) {
	var service *Service
	if got := service.IngestProviderKey(context.Background(), ingestProviderAlchemy, ingestEnvKey); got != ingestEnvKey {
		t.Fatal("a nil settings service did not keep the environment key")
	}
	if got := newTestService(newMemoryStore()).IngestProviderKey(nil, ingestProviderAlchemy, ingestEnvKey); got != ingestEnvKey {
		t.Fatal("a nil context did not keep the environment key")
	}
}

type refuseIngestSealer struct{}

func (refuseIngestSealer) Seal(string) (string, error) {
	return "", errors.New("seal refused")
}

func (refuseIngestSealer) Open(string) (string, error) {
	return "", errors.New("open refused")
}
