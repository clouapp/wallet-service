package chaincatalog_test

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/repositories/chaincatalog"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const (
	sepoliaUSDC    = "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238"
	sepoliaLINK    = "0x779877A7B0D9E8603169DdbD7836e478b4624789"
	amoyUSDC       = "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582"
	amoyLINK       = "0x0Fd9e8d3aF1aaee056EB9e802c3A762a667b1904"
	devnetUSDC     = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"
	wrappedSOLMint = "So11111111111111111111111111111111111111112"
)

func TestToken_Alignment_DoesNotQueryOutsideTheRepository(t *testing.T) {
	source, err := os.ReadFile("token_alignment.go")
	if err != nil {
		t.Fatalf("read token alignment: %v", err)
	}
	for _, needle := range []string{"facades.Orm", "Orm()", ".Query()", ".Exec(", ".Raw("} {
		if strings.Contains(string(source), needle) {
			t.Fatalf("token alignment queries outside the repository (%s)", needle)
		}
	}
}

// activeContracts lists a chain's active token contracts, sorted.
func activeContracts(t *testing.T, ctx context.Context, chainID string) []string {
	t.Helper()
	tokens, err := repositories.NewTokenRepository(nil).FindByChainID(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	contracts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		contracts = append(contracts, token.ContractAddress)
	}
	sort.Strings(contracts)
	return contracts
}

func sorted(values ...string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func assertActive(t *testing.T, ctx context.Context, chainID string, want ...string) {
	t.Helper()
	if got := activeContracts(t, ctx, chainID); strings.Join(got, ",") != strings.Join(sorted(want...), ",") {
		t.Errorf("%s active tokens %v, want %v", chainID, got, sorted(want...))
	}
}

func TestSeed_Tokens_TheTestnetProfileSeedsTheTestnetContractsOfThePrimaryRecords(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	restoreChainNetworkProfile(t, models.ChainNetworkProfileTestnet)
	if err := newCatalog().SeedChains(ctx); err != nil {
		t.Fatalf("seed chains: %v", err)
	}
	if err := newCatalog().SeedTokens(ctx); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}

	assertActive(t, ctx, models.ChainETH, sepoliaUSDC, sepoliaLINK)
	assertActive(t, ctx, models.ChainPolygon, amoyUSDC, amoyLINK)
	assertActive(t, ctx, models.ChainSOL, devnetUSDC, wrappedSOLMint)
	assertActive(t, ctx, models.ChainTron, models.USDTContractTronNile)

	aligned, err := newCatalog().AlignNetworkTokens(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(aligned.Changes) != 0 {
		t.Fatalf("a database seeded on the testnet profile is already aligned, got %+v", aligned.Changes)
	}
}

func TestAlign_Network_TokensMovesTheFlippedPrimaryRecordsOntoTestnetContracts(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	restoreChainNetworkProfile(t, models.ChainNetworkProfileTestnet)
	if err := newCatalog().SeedChains(ctx); err != nil {
		t.Fatalf("seed chains: %v", err)
	}
	// A database seeded before the primary records chose their tokens by network: the
	// records point at the testnets, the tokens are the mainnet ones.
	restoreChainNetworkProfile(t, models.ChainNetworkProfileMainnet)
	if err := newCatalog().SeedTokens(ctx); err != nil {
		t.Fatalf("seed mainnet tokens: %v", err)
	}
	restoreChainNetworkProfile(t, models.ChainNetworkProfileTestnet)
	tokens := repositories.NewTokenRepository(nil)
	custom := &models.Token{ID: uuid.New(), ChainID: models.ChainETH, Symbol: "TST", Name: "Operator test token", ContractAddress: "0x00000000000000000000000000000000000000a1", Decimals: 18, Status: models.TokenStatusActive}
	if err := tokens.Create(ctx, custom); err != nil {
		t.Fatal(err)
	}
	tethBefore := activeContracts(t, ctx, models.ChainTETH)
	tsolBefore := activeContracts(t, ctx, models.ChainTSOL)

	dry, err := newCatalog().AlignNetworkTokens(ctx, false)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	perChain := map[string]map[string]int{}
	for _, change := range dry.Changes {
		if perChain[change.ChainID] == nil {
			perChain[change.ChainID] = map[string]int{}
		}
		perChain[change.ChainID][change.Action]++
	}
	for chainID, want := range map[string]map[string]int{
		models.ChainETH:      {chaincatalog.TokenActionCreate: 2, chaincatalog.TokenActionDeactivate: 7},
		models.ChainPolygon:  {chaincatalog.TokenActionCreate: 2, chaincatalog.TokenActionDeactivate: 6},
		models.ChainSOL:      {chaincatalog.TokenActionCreate: 1, chaincatalog.TokenActionDeactivate: 5},
		models.ChainBase:     {chaincatalog.TokenActionCreate: 1, chaincatalog.TokenActionDeactivate: 1},
		models.ChainArbitrum: {chaincatalog.TokenActionCreate: 1, chaincatalog.TokenActionDeactivate: 2},
		models.ChainBSC:      {chaincatalog.TokenActionDeactivate: 2},
		models.ChainTron:     {chaincatalog.TokenActionCreate: 1, chaincatalog.TokenActionDeactivate: 1},
	} {
		got := perChain[chainID]
		if len(got) != len(want) || got[chaincatalog.TokenActionCreate] != want[chaincatalog.TokenActionCreate] ||
			got[chaincatalog.TokenActionDeactivate] != want[chaincatalog.TokenActionDeactivate] {
			t.Errorf("%s planned %v, want %v", chainID, got, want)
		}
		delete(perChain, chainID)
	}
	if len(perChain) != 0 {
		t.Errorf("no other chain may change, got %v", perChain)
	}
	if len(dry.Unknown) != 1 || !strings.Contains(dry.Unknown[0], custom.ContractAddress) {
		t.Errorf("the operator's token is reported and left alone, got %v", dry.Unknown)
	}
	if got := activeContracts(t, ctx, models.ChainETH); len(got) != 8 {
		t.Fatalf("a dry run writes nothing, eth has %v", got)
	}

	applied, err := newCatalog().AlignNetworkTokens(ctx, true)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied.Changes) != len(dry.Changes) {
		t.Fatalf("apply made %d changes, the dry run planned %d", len(applied.Changes), len(dry.Changes))
	}
	assertActive(t, ctx, models.ChainETH, sepoliaUSDC, sepoliaLINK, custom.ContractAddress)
	assertActive(t, ctx, models.ChainPolygon, amoyUSDC, amoyLINK)
	assertActive(t, ctx, models.ChainSOL, devnetUSDC, wrappedSOLMint)
	assertActive(t, ctx, models.ChainBase, models.USDCContractBaseSepolia)
	assertActive(t, ctx, models.ChainArbitrum, models.USDCContractArbSepolia)
	assertActive(t, ctx, models.ChainBSC)
	assertActive(t, ctx, models.ChainTron, models.USDTContractTronNile)
	if got := activeContracts(t, ctx, models.ChainTETH); strings.Join(got, ",") != strings.Join(tethBefore, ",") {
		t.Errorf("teth is a test record under every profile and is left as it is: %v, was %v", got, tethBefore)
	}
	if got := activeContracts(t, ctx, models.ChainTSOL); strings.Join(got, ",") != strings.Join(tsolBefore, ",") {
		t.Errorf("tsol is left as it is: %v, was %v", got, tsolBefore)
	}
	all, err := tokens.FindAllByChainID(ctx, models.ChainETH)
	if err != nil {
		t.Fatal(err)
	}
	inactive := 0
	for _, token := range all {
		if token.Status == models.TokenStatusInactive {
			inactive++
		}
	}
	if len(all) != 10 || inactive != 7 {
		t.Fatalf("the mainnet rows stay, inactive: %d rows, %d inactive", len(all), inactive)
	}

	again, err := newCatalog().AlignNetworkTokens(ctx, true)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if len(again.Changes) != 0 {
		t.Fatalf("a second run changes nothing, got %+v", again.Changes)
	}
}

func TestAlign_Network_TokensLeavesAMainnetDatabaseAsItIs(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	// No profile: the primary records are seeded on their mainnets, as in production.
	restoreChainNetworkProfile(t, "")
	if err := newCatalog().SeedChains(ctx); err != nil {
		t.Fatalf("seed chains: %v", err)
	}
	if err := newCatalog().SeedTokens(ctx); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}
	chains, err := repositories.NewChainRepository(nil).FindAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := map[string][]string{}
	for _, chain := range chains {
		before[chain.ID] = activeContracts(t, ctx, chain.ID)
	}

	result, err := newCatalog().AlignNetworkTokens(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) != 0 || len(result.Mainnet) != len(models.PrimaryChainIDs) {
		t.Fatalf("a mainnet database is not touched: changes %+v, mainnet records %v", result.Changes, result.Mainnet)
	}
	for _, chain := range chains {
		if got := activeContracts(t, ctx, chain.ID); strings.Join(got, ",") != strings.Join(before[chain.ID], ",") {
			t.Errorf("%s tokens changed: %v, were %v", chain.ID, got, before[chain.ID])
		}
	}
}
