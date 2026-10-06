package jobs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestEachWalletJobRoundTripsPayloadCallsOneServiceAndCarriesNoCredential
// covers the wallet jobs that had a service-call test and no payload
// round-trip or credential check. SendCredentialMailJob already has both.
func TestEach_Wallet_JobRoundTripsPayloadCallsOneServiceAndCarriesNoCredential(t *testing.T) {
	walletID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	const chainID = "eth"
	args, err := WalletArgs(walletID.String(), chainID)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 1 || args[0].Type != "string" {
		t.Fatalf("args = %#v", args)
	}
	encoded, ok := args[0].Value.(string)
	if !ok {
		t.Fatalf("payload %#v is not text", args[0].Value)
	}
	assertPayloadCarriesNoCredential(t, encoded)

	var document walletDocument
	if err := json.Unmarshal([]byte(encoded), &document); err != nil {
		t.Fatal(err)
	}
	if document.WalletID != walletID.String() || document.ChainID != chainID {
		t.Fatalf("document = %#v", document)
	}
	decoded, err := decodeWalletPayload("round-trip", []any{encoded})
	if err != nil {
		t.Fatal(err)
	}
	if decoded.WalletID != walletID || decoded.ChainID != chainID {
		t.Fatalf("decoded = %#v", decoded)
	}

	jobs := []struct {
		name   string
		method string
		handle func(walletQueue) error
	}{
		{"refresh_wallet_balances", "RefreshBalances", func(w walletQueue) error {
			return (&RefreshWalletBalances{wallets: w}).Handle(encoded)
		}},
		{"refresh_wallet_transactions", "RefreshTransactions", func(w walletQueue) error {
			return (&RefreshWalletTransactions{wallets: w}).Handle(encoded)
		}},
		{"refresh_wallet_tokens", "RefreshTokens", func(w walletQueue) error {
			return (&RefreshWalletTokens{wallets: w}).Handle(encoded)
		}},
		{"refresh_wallet_utxos", "RefreshUTXOs", func(w walletQueue) error {
			return (&RefreshWalletUTXOs{wallets: w}).Handle(encoded)
		}},
		{"reconcile_wallet_state", "ReconcileWallet", func(w walletQueue) error {
			return (&ReconcileWalletState{wallets: w}).Handle(encoded)
		}},
	}
	for _, tc := range jobs {
		t.Run(tc.name, func(t *testing.T) {
			assertPayloadCarriesNoCredential(t, encoded)
			rec := &calledWallet{}
			if err := tc.handle(rec); err != nil {
				t.Fatal(err)
			}
			if rec.calls != 1 || rec.method != tc.method || rec.id != walletID || rec.chain != chainID {
				t.Fatalf("service call = %d %s %s %s", rec.calls, rec.method, rec.id, rec.chain)
			}
		})
	}
}

func assertPayloadCarriesNoCredential(t *testing.T, payload string) {
	t.Helper()
	for _, marker := range []string{
		"token",
		"passphrase",
		"share",
		"secret",
		"secret-value",
		"<!DOCTYPE html>",
		"Reset Your Password",
		"Accept Invitation",
		"@",
	} {
		if strings.Contains(payload, marker) {
			t.Fatalf("payload %q carries a credential %q", payload, marker)
		}
	}
}

type calledWallet struct {
	calls  int
	method string
	id     uuid.UUID
	chain  string
}

func (c *calledWallet) note(method string, id uuid.UUID, chain string) error {
	c.calls++
	c.method = method
	c.id = id
	c.chain = chain
	return nil
}

func (c *calledWallet) RefreshBalances(_ context.Context, id uuid.UUID, chain string) error {
	return c.note("RefreshBalances", id, chain)
}

func (c *calledWallet) RefreshTransactions(_ context.Context, id uuid.UUID, chain string) error {
	return c.note("RefreshTransactions", id, chain)
}

func (c *calledWallet) RefreshTokens(_ context.Context, id uuid.UUID, chain string) error {
	return c.note("RefreshTokens", id, chain)
}

func (c *calledWallet) RefreshUTXOs(_ context.Context, id uuid.UUID, chain string) error {
	return c.note("RefreshUTXOs", id, chain)
}

func (c *calledWallet) ReconcileWallet(_ context.Context, id uuid.UUID, chain string) error {
	return c.note("ReconcileWallet", id, chain)
}
