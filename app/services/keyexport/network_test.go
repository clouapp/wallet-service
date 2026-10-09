package keyexport

import (
	"context"
	"errors"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

type chainSourceStub struct {
	record *models.Chain
	err    error
}

func (s chainSourceStub) FindByID(context.Context, string) (*models.Chain, error) {
	return s.record, s.err
}

func TestChain_NetworkResolver_RefusesAMissingChainRecord(t *testing.T) {
	for name, source := range map[string]chainSourceStub{
		"error": {err: errors.New("db down")},
		"nil":   {},
	} {
		_, err := ChainNetworkResolver{Chains: source}.ResolveNetwork(context.Background(), "sol")
		if err == nil || err.Error() != "chain record sol not found" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := (ChainNetworkResolver{}).ResolveNetwork(context.Background(), "sol"); err == nil {
		t.Error("a resolver without a chain source must refuse")
	}
}

func TestChain_NetworkResolver_OpensTheRPCURLOnlyWhereItNamesTheNetwork(t *testing.T) {
	var opened []string
	decrypt := func(sealed string) (string, error) {
		opened = append(opened, sealed)
		return "https://api.devnet.solana.com", nil
	}

	solana, err := ChainNetworkResolver{
		Chains:     chainSourceStub{record: &models.Chain{ID: "sol", AdapterType: models.AdapterTypeSolana, RpcURL: "sealed"}},
		DecryptRPC: decrypt,
	}.ResolveNetwork(context.Background(), "sol")
	if err != nil || solana.Name != models.NetworkSolanaDevnet || !solana.Testnet || solana.AdapterType != models.AdapterTypeSolana {
		t.Fatalf("solana: %+v, %v", solana, err)
	}

	if _, err := (ChainNetworkResolver{
		Chains:     chainSourceStub{record: &models.Chain{ID: "eth", AdapterType: models.AdapterTypeEVM, RpcURL: "sealed-eth"}},
		DecryptRPC: decrypt,
	}).ResolveNetwork(context.Background(), "eth"); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != "sealed" {
		t.Fatalf("opened %v, want only the Solana URL", opened)
	}
}

func TestChain_NetworkResolver_AnUnreadableRPCURLFallsBackToTheRecord(t *testing.T) {
	resolved, err := ChainNetworkResolver{
		Chains:     chainSourceStub{record: &models.Chain{ID: "sol", AdapterType: models.AdapterTypeSolana, IsTestnet: true, RpcURL: "sealed"}},
		DecryptRPC: func(string) (string, error) { return "", errors.New("bad seal") },
	}.ResolveNetwork(context.Background(), "sol")
	if err != nil || !resolved.Testnet {
		t.Fatalf("resolved %+v, %v", resolved, err)
	}
}
