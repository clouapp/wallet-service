//go:build smoke

// This file is a live smoke against api.sandbox.bridge.xyz. It is behind the
// `smoke` build tag and skips without a key, so it never runs in CI and never
// runs by accident.
//
//	BRIDGE_API_KEY=<sandbox key> go test -tags smoke ./app/adapters/ramp/bridge/ \
//	    -run TestVirtualAccountSmoke -v -timeout 10m
//
// It exists because the unit tests speak to httptest and prove only that the
// adapter is consistent with our reading of the provider. Five mapping bugs
// have reached this package that way. When BRL is enabled on the account, run
// it with -smoke.currency=brl and the deposit instructions should carry a
// br_code.
package bridge_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/bridge"
)

var (
	smokeCurrency = flag.String("smoke.currency", "usd", "source currency to provision")
	smokeCustomer = flag.String("smoke.customer", "2980af75-cd7a-4abf-af0d-9c262f7dec12", "sandbox customer id")
	smokeAmount   = flag.String("smoke.amount", "137.50", "amount to simulate depositing")
)

func TestVirtualAccountSmoke(t *testing.T) {
	apiKey := os.Getenv("BRIDGE_API_KEY")
	if apiKey == "" {
		t.Skip("BRIDGE_API_KEY not set")
	}
	// The package's TestMain already binds the mocked log facade, which is all
	// the transport needs; booting the framework here would build a second
	// application over it.

	client := bridge.NewClient(bridge.Config{BaseURL: bridge.SandboxBaseURL, APIKey: apiKey})
	adapter := bridge.New(func(context.Context) (*bridge.Client, error) { return client, nil }, nil)

	ctx := context.Background()
	ref := ramp.CustomerRef{CustomerID: 1, ExternalID: *smokeCustomer}
	destination := ramp.CryptoLeg{
		Currency: "USDC", PaymentRail: "ethereum",
		Address: "0x3f5CE5FBFe3E9af3971dD833D26BA9b5C936f0bE",
	}

	account, err := adapter.ProvisionVirtualAccount(ctx, ref, ramp.VirtualAccountRequest{
		SourceCurrency: *smokeCurrency, Destination: destination, FeePercent: "1.5",
	})
	require.NoError(t, err, "provision")
	t.Logf("account %s status=%s currency=%s", account.ProviderAccountID, account.Status, account.SourceCurrency)
	for k, v := range account.DepositInstructions {
		t.Logf("  %s: %v", k, v)
	}

	// Bridge does not deduplicate virtual accounts — verified, and the reason
	// the adapter reads before it creates. A second account here means one user
	// with two deposit codes.
	again, err := adapter.ProvisionVirtualAccount(ctx, ref, ramp.VirtualAccountRequest{
		SourceCurrency: *smokeCurrency, Destination: destination, FeePercent: "1.5",
	})
	require.NoError(t, err, "re-provision")
	require.Equal(t, account.ProviderAccountID, again.ProviderAccountID,
		"read-then-create must return the existing account")

	fetched, err := adapter.GetVirtualAccount(ctx, ref, account.ProviderAccountID)
	require.NoError(t, err, "get")
	require.Equal(t, "active", fetched.Status)
	require.NotEmpty(t, fetched.DepositInstructions)

	// The user has the deposit code saved in their bank app; an update must not
	// rotate it.
	before, _ := json.Marshal(fetched.DepositInstructions)
	updated, err := adapter.UpdateVirtualAccount(ctx, ref, account.ProviderAccountID,
		ramp.VirtualAccountUpdate{FeePercent: "2.25"})
	require.NoError(t, err, "update")
	after, _ := json.Marshal(updated.DepositInstructions)
	require.Equal(t, "2.25", updated.FeePercent)
	require.JSONEq(t, string(before), string(after), "deposit instructions must survive an update")

	depositID := simulateDeposit(t, client, *smokeCustomer, account.ProviderAccountID, *smokeAmount, *smokeCurrency)
	t.Logf("deposit %s", depositID)

	var deposit *ramp.VirtualAccountDeposit
	require.Eventually(t, func() bool {
		deposit, err = adapter.GetDeposit(ctx, ref, account.ProviderAccountID, depositID)
		return err == nil && deposit != nil && deposit.Status == "completed"
	}, 2*time.Minute, 6*time.Second, "deposit never settled")

	// `amount` names the fiat leg on funds_received and the crypto leg on the
	// payment events. A mapping that read it would report the crypto figure as
	// the fiat one.
	require.True(t, sameAmount(deposit.SourceAmount, *smokeAmount),
		"source amount %q must be the fiat sent", deposit.SourceAmount)
	require.False(t, sameAmount(deposit.DestinationAmount, *smokeAmount),
		"destination amount %q must be the crypto delivered", deposit.DestinationAmount)
	require.NotEmpty(t, deposit.FeeAmount, "developer fee")
	require.NotEmpty(t, deposit.TxHash, "settlement hash")
	t.Logf("fiat=%s crypto=%s fee=%s sender=%q",
		deposit.SourceAmount, deposit.DestinationAmount, deposit.FeeAmount, deposit.SenderName)
}

// simulateDeposit drives the sandbox-only endpoint, which the adapter has no
// business knowing about, and reads the deposit id back off the history.
func simulateDeposit(t *testing.T, client *bridge.Client, customerID, accountID, amount, currency string) string {
	t.Helper()
	ctx := context.Background()

	var ack json.RawMessage
	require.NoError(t, client.Do(ctx, bridge.Request{
		Method: http.MethodPost,
		Path:   fmt.Sprintf("/customers/%s/virtual_accounts/%s/simulate_deposit", customerID, accountID),
		Body:   map[string]string{"amount": amount, "currency": currency},
		// A successful key is refused after 24h (`Idempotency Key Retry Deadline
		// Exceeded`). The Wave 0 run already consumed smoke-<account>-<amount>,
		// so each invocation needs a fresh key — not a replay of the last one.
		IdempotencyKey: fmt.Sprintf("smoke-%s-%s-%d", accountID, amount, time.Now().UnixNano()),
	}, &ack), "simulate deposit")

	var history struct {
		Data []struct {
			Type      string `json:"type"`
			DepositID string `json:"deposit_id"`
			Amount    string `json:"amount"`
			CreatedAt string `json:"created_at"`
		} `json:"data"`
	}
	var depositID string
	require.Eventually(t, func() bool {
		if err := client.Do(ctx, bridge.Request{
			Method: http.MethodGet,
			Path:   fmt.Sprintf("/customers/%s/virtual_accounts/%s/history?limit=25", customerID, accountID),
		}, &history); err != nil {
			return false
		}
		var newest string
		found := false
		for _, a := range history.Data {
			// Bridge echoes its own normalization: "137.50" comes back "137.5".
			if a.Type == "funds_received" && a.DepositID != "" && sameAmount(a.Amount, amount) {
				if !found || a.CreatedAt > newest {
					newest = a.CreatedAt
					depositID = a.DepositID
					found = true
				}
			}
		}
		return found
	}, time.Minute, 5*time.Second, "no funds_received for %s appeared", amount)
	return depositID
}

// sameAmount compares decimal strings by value.
func sameAmount(a, b string) bool {
	x, okA := new(big.Rat).SetString(a)
	y, okB := new(big.Rat).SetString(b)
	return okA && okB && x.Cmp(y) == 0
}
