package chainregistry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

type scriptedChains struct {
	calls int
	rows  [][]models.Chain
	errs  []error
}

func (s *scriptedChains) FindActive(context.Context) ([]models.Chain, error) {
	i := s.calls
	s.calls++
	if i >= len(s.errs) {
		return nil, errors.New("unexpected find")
	}
	if s.errs[i] != nil {
		return nil, s.errs[i]
	}
	return s.rows[i], nil
}

func recordingInstaller(seen *[]string) CatalogInstaller {
	return func(reg *chain.Registry, rows []models.Chain, _ map[string][]types.Token) map[string]string {
		networks := map[string]string{}
		for _, row := range rows {
			*seen = append(*seen, row.RpcURL)
			reg.RegisterChain(mocks.NewMockChain(row.ID))
			networks[row.ID] = "net-" + row.ID
		}
		return networks
	}
}

func TestLoad_Builds_FromTheCachedSealedCatalog(t *testing.T) {
	const sealed = "sealed-rpc"
	cache := NewCatalogCache()
	cache.Put(catalogCacheKey, []models.Chain{{ID: "eth", RpcURL: sealed}})
	stored := cacheMust(t, cache)
	stored[0].RpcURL = "https://rpc.example/plaintext"
	if got, _ := cache.Get(catalogCacheKey); got[0].RpcURL != sealed {
		t.Fatalf("cache rpc = %q", got[0].RpcURL)
	}

	var seen []string
	store := &scriptedChains{}
	svc := NewChainRegistryService(ChainRegistryDeps{
		Store:    store,
		Cache:    cache,
		Install:  recordingInstaller(&seen),
		Registry: chain.NewRegistry(),
	})
	tokens := map[string][]types.Token{
		"eth": {{Symbol: "USDT", Contract: "0xabc", Decimals: 6, ChainID: "eth"}},
	}
	if err := svc.Load(tokens); err != nil {
		t.Fatal(err)
	}
	if store.calls != 0 {
		t.Fatalf("load queried the store %d times", store.calls)
	}
	if len(seen) != 1 || seen[0] != sealed {
		t.Fatalf("installer saw %#v", seen)
	}
	got, err := svc.Registry().Chain("eth")
	if err != nil || got.ID() != "eth" {
		t.Fatalf("chain = %v err=%v", got, err)
	}
	listed := svc.Registry().TokensForChain("eth")
	if len(listed) != 1 || listed[0].Symbol != "USDT" {
		t.Fatalf("tokens = %#v", listed)
	}
	if svc.Networks()["eth"] != "net-eth" {
		t.Fatalf("networks = %#v", svc.Networks())
	}
	if err := svc.Load(nil); err != nil || store.calls != 0 {
		t.Fatalf("second load err=%v calls=%d", err, store.calls)
	}
}

func TestRefresh_Reloads_AndDropsARemovedChain(t *testing.T) {
	const sealed = "sealed-rpc"
	var seen []string
	store := &scriptedChains{
		rows: [][]models.Chain{
			{{ID: "eth", RpcURL: sealed}, {ID: "btc", RpcURL: sealed}},
			{{ID: "btc", RpcURL: sealed}},
		},
		errs: []error{nil, nil},
	}
	svc := NewChainRegistryService(ChainRegistryDeps{
		Store:    store,
		Cache:    NewCatalogCache(),
		Install:  recordingInstaller(&seen),
		Registry: chain.NewRegistry(),
	})
	if err := svc.Refresh(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Registry().Chain("eth"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Refresh(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Registry().Chain("eth"); !errors.Is(err, ErrUnknownChain) {
		t.Fatalf("removed chain err = %v", err)
	}
	if _, err := svc.Registry().Chain("btc"); err != nil {
		t.Fatal(err)
	}
	if store.calls != 2 {
		t.Fatalf("store calls = %d", store.calls)
	}
	for _, url := range seen {
		if url != sealed {
			t.Fatalf("installer saw %q", url)
		}
	}
	cached, found := svc.cache.Get(catalogCacheKey)
	if !found || len(cached) != 1 || cached[0].RpcURL != sealed {
		t.Fatalf("cache = %#v found=%v", cached, found)
	}
}

func TestRefresh_Keeps_ThePreviousCatalogWhenALaterReadFails(t *testing.T) {
	store := &scriptedChains{
		rows: [][]models.Chain{{{ID: "eth", RpcURL: "sealed-rpc"}}},
		errs: []error{nil, errors.New("db down")},
	}
	var seen []string
	svc := NewChainRegistryService(ChainRegistryDeps{
		Store:    store,
		Install:  recordingInstaller(&seen),
		Registry: chain.NewRegistry(),
	})
	if err := svc.Refresh(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	err := svc.Refresh(context.Background(), nil)
	if err == nil || err.Error() != "db down" || errors.Is(err, ErrUnknownChain) {
		t.Fatalf("err = %v", err)
	}
	if _, chainErr := svc.Registry().Chain("eth"); chainErr != nil {
		t.Fatal(chainErr)
	}
}

func TestFirst_Refresh_FailureLeavesAnEmptyCatalog(t *testing.T) {
	store := &scriptedChains{errs: []error{errors.New("db down")}}
	var seen []string
	svc := NewChainRegistryService(ChainRegistryDeps{
		Store:    store,
		Install:  recordingInstaller(&seen),
		Registry: chain.NewRegistry(),
	})
	err := svc.Refresh(context.Background(), nil)
	if err == nil || strings.Contains(err.Error(), catalogCacheKey) || errors.Is(err, ErrUnknownChain) {
		t.Fatalf("err = %v", err)
	}
	if _, chainErr := svc.Registry().Chain("eth"); !errors.Is(chainErr, ErrUnknownChain) {
		t.Fatalf("missing chain err = %v", chainErr)
	}
	if err := svc.Load(nil); err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d", store.calls)
	}
}

func TestRefresh_Does_NotLogTheURLOrTheCacheKey(t *testing.T) {
	const sealed = "sealed-rpc-value"
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	store := &scriptedChains{
		rows: [][]models.Chain{{{ID: "eth", RpcURL: sealed}}},
		errs: []error{nil},
	}
	var seen []string
	svc := NewChainRegistryService(ChainRegistryDeps{
		Store:    store,
		Install:  recordingInstaller(&seen),
		Registry: chain.NewRegistry(),
	})
	if err := svc.Refresh(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	logged := buf.String()
	if strings.Contains(logged, sealed) || strings.Contains(logged, catalogCacheKey) {
		t.Fatalf("log = %q", logged)
	}
}

func cacheMust(t *testing.T, cache CatalogCache) []models.Chain {
	t.Helper()
	rows, found := cache.Get(catalogCacheKey)
	if !found || len(rows) != 1 {
		t.Fatalf("cache = %#v found=%v", rows, found)
	}
	return rows
}
