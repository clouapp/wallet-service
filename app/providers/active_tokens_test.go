package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/macrowallets/waas/app/models"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
)

func resetActiveTokens() {
	activeTokenMu.Lock()
	activeTokensReady = false
	activeTokenRows = nil
	activeTokenMu.Unlock()
}

type staticActiveTokens struct {
	rows []models.Token
	err  error
}

func (s staticActiveTokens) FindActive(context.Context) ([]models.Token, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.rows, nil
}

func TestRead_ActiveTokens_StoresTheCatalogFromFindActive(t *testing.T) {
	resetActiveTokens()
	t.Cleanup(resetActiveTokens)

	readActiveTokens(staticActiveTokens{rows: []models.Token{{
		ChainID:         "eth",
		Symbol:          "usdt",
		Name:            "Tether",
		ContractAddress: "0xabc",
		Decimals:        6,
		Status:          "active",
	}}})

	rows, ok := bootedActiveTokens()
	if !ok || len(rows) != 1 || rows[0].Symbol != "usdt" || rows[0].ContractAddress != "0xabc" {
		t.Fatalf("catalog = %+v ok=%v", rows, ok)
	}
}

func TestRead_ActiveTokens_LeavesAnEmptyCatalogWhenTheReadFails(t *testing.T) {
	resetActiveTokens()
	t.Cleanup(resetActiveTokens)

	readActiveTokens(staticActiveTokens{err: errors.New("db down")})

	rows, ok := bootedActiveTokens()
	if !ok || len(rows) != 0 {
		t.Fatalf("catalog = %+v ok=%v", rows, ok)
	}
}

func TestBooted_ActiveTokens_NotLoadedBeforeBoot(t *testing.T) {
	resetActiveTokens()
	t.Cleanup(resetActiveTokens)

	rows, ok := bootedActiveTokens()
	if ok || rows != nil {
		t.Fatalf("catalog = %+v ok=%v", rows, ok)
	}
}

func TestRegister_ActiveTokens_KeepsTheSameActiveSet(t *testing.T) {
	reg := chainpkg.NewRegistry()
	byChain := registerActiveTokens(reg, []models.Token{
		{ChainID: "eth", Symbol: "usdt", Name: "Tether", ContractAddress: "0xabc", Decimals: 6},
		{ChainID: "eth", Symbol: "usdc", Name: "USD Coin", ContractAddress: "0xdef", Decimals: 6},
		{ChainID: "sol", Symbol: "usdc", Name: "USD Coin", ContractAddress: "mint", Decimals: 6},
	})

	if len(byChain["eth"]) != 2 || byChain["eth"][0].Contract != "0xabc" || byChain["eth"][1].Symbol != "usdc" {
		t.Fatalf("eth adapter catalog = %+v", byChain["eth"])
	}
	if len(byChain["sol"]) != 1 || byChain["sol"][0].Contract != "mint" || byChain["sol"][0].Decimals != 6 {
		t.Fatalf("sol adapter catalog = %+v", byChain["sol"])
	}

	usdt, err := reg.FindToken("eth", "usdt")
	if err != nil || usdt.Contract != "0xabc" || usdt.Decimals != 6 {
		t.Fatalf("registry usdt = %+v err=%v", usdt, err)
	}
	sol, err := reg.FindToken("sol", "usdc")
	if err != nil || sol.Contract != "mint" {
		t.Fatalf("registry sol = %+v err=%v", sol, err)
	}
}
