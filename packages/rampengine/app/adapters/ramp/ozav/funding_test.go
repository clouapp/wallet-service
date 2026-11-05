package ozav

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

var fundingCustomer = ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}

// fundingRequest asks whether the wallet holds amount of currency on chain.
func fundingRequest(currency, chain, amount string) ramp.OfframpFundingRequest {
	req := ramp.OfframpFundingRequest{OfframpCreateRequest: offrampCreate()}
	req.Source.Currency = currency
	req.Source.PaymentRail = chain
	req.Source.Amount = amount
	return req
}

// fundingRoutes answers the wallet lookup and the balances with body.
func fundingRoutes(balances fakeReply) map[string]fakeReply {
	return map[string]fakeReply{
		"GET /wallets":                     {body: oneManagedWallet},
		"GET /wallets/ozav_wal_1/balances": balances,
	}
}

func TestOfframpFundingSatisfiedWhenBalanceCoversOrder(t *testing.T) {
	adapter, _ := newFakeAdapter(t, offrampRoutes())
	req := ramp.OfframpFundingRequest{OfframpCreateRequest: offrampCreate()}
	ok, err := adapter.OfframpFundingSatisfied(context.Background(), fundingCustomer, req)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestOfframpFundingSatisfied(t *testing.T) {
	cases := []struct {
		name     string
		balances string
		req      ramp.OfframpFundingRequest
		want     bool
		wantErr  string
	}{
		{
			// The live shape: amount only, no chain.
			name:     "amount-only balance covers",
			balances: `{"data":[{"currency":"USDC","amount":"10"}]}`,
			req:      fundingRequest("usdc", "solana", "10"), want: true,
		},
		{
			name:     "a balance on the order's chain wins over a chainless one",
			balances: `{"data":[{"currency":"USDC","amount":"1"},{"currency":"USDC","chain":"solana","available":"20"}]}`,
			req:      fundingRequest("USDC", "solana", "15"), want: true,
		},
		{
			name:     "a chainless balance answers when no entry names the chain",
			balances: `{"data":[{"currency":"USDC","chain":"ethereum","available":"99"},{"currency":"USDC","amount":"5"}]}`,
			req:      fundingRequest("USDC", "solana", "6"), want: false,
		},
		{
			name:     "insufficient",
			balances: `{"data":[{"currency":"USDC","amount":"9.999999"}]}`,
			req:      fundingRequest("USDC", "solana", "10"), want: false,
		},
		{
			name:     "the currency is not held at all",
			balances: `{"data":[{"currency":"BRS","amount":"500"}]}`,
			req:      fundingRequest("USDC", "solana", "1"), want: false,
		},
		{
			name:     "no required amount",
			balances: `{"data":[{"currency":"USDC","amount":"10"}]}`,
			req:      fundingRequest("USDC", "solana", " "), wantErr: "requires a source amount",
		},
		{
			name:     "an unreadable balance",
			balances: `{"data":[{"currency":"USDC","amount":"lots"}]}`,
			req:      fundingRequest("USDC", "solana", "1"), wantErr: "parse available balance",
		},
		{
			name:     "an unreadable required amount",
			balances: `{"data":[{"currency":"USDC","amount":"10"}]}`,
			req:      fundingRequest("USDC", "solana", "ten"), wantErr: "parse required amount",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			adapter, _ := newFakeAdapter(t, fundingRoutes(fakeReply{body: c.balances}))

			ok, err := adapter.OfframpFundingSatisfied(context.Background(), fundingCustomer, c.req)

			if c.wantErr != "" {
				require.ErrorContains(t, err, c.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.want, ok)
		})
	}
}

// A balances call that fails is not an empty wallet: the caller must not
// expire an order on it.
func TestOfframpFundingSatisfiedReportsAFailedBalanceRead(t *testing.T) {
	adapter, _ := newFakeAdapter(t, fundingRoutes(fakeReply{status: http.StatusServiceUnavailable,
		body: `{"error":{"type":"api_error","code":"LOOKUP_UNAVAILABLE"}}`}))

	_, err := adapter.OfframpFundingSatisfied(context.Background(), fundingCustomer, fundingRequest("USDC", "solana", "1"))

	require.ErrorIs(t, err, ramp.ErrRetryableUpstream)
}

func TestOfframpFundingInstructions(t *testing.T) {
	cases := []struct {
		name      string
		addresses string
		req       ramp.OfframpFundingRequest
		want      string
		wantErr   error
		wantMsg   string
	}{
		{
			name:      "the address for the order's token and chain",
			addresses: `{"data":[{"address":"EthAddr","network":"ethereum","currency":"USDC"},{"address":"SolAddr","network":"solana","currency":"USDC"}]}`,
			req:       fundingRequest("usdc", "Solana", "10"), want: "SolAddr",
		},
		{
			name:      "an address that names no currency serves its chain",
			addresses: `{"data":[{"address":"SolAny","network":"solana"}]}`,
			req:       fundingRequest("USDC", "solana", "10"), want: "SolAny",
		},
		{
			name:      "no address for the token",
			addresses: `{"data":[{"address":"SolBrs","network":"solana","currency":"BRS"},{"address":"","network":"solana","currency":"USDC"}]}`,
			req:       fundingRequest("USDC", "solana", "10"), wantErr: ramp.ErrProviderFailed,
		},
		{
			name:      "no amount to fund",
			addresses: ozavDepositAddressBody,
			req:       fundingRequest("USDC", "solana", ""), wantMsg: "requires a source amount",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			adapter, _ := newFakeAdapter(t, map[string]fakeReply{
				"GET /wallets": {body: oneManagedWallet},
				"GET /wallets/ozav_wal_1/deposit-addresses": {body: c.addresses},
			})

			got, err := adapter.OfframpFundingInstructions(context.Background(), fundingCustomer, c.req)

			switch {
			case c.wantErr != nil:
				require.ErrorIs(t, err, c.wantErr)
			case c.wantMsg != "":
				require.ErrorContains(t, err, c.wantMsg)
			default:
				require.NoError(t, err)
				require.Equal(t, c.want, got.ToAddress)
				require.Equal(t, "usdc", got.Currency)
				require.Equal(t, c.req.Source.Amount, got.Amount)
			}
		})
	}
}

// Without a managed wallet there is no address to give, and nothing was sent
// that a retry would repeat.
func TestOfframpFundingInstructionsWithoutAWallet(t *testing.T) {
	adapter, _ := newFakeAdapter(t, map[string]fakeReply{"GET /wallets": {body: `{"data":[]}`}})

	_, err := adapter.OfframpFundingInstructions(context.Background(), fundingCustomer, fundingRequest("USDC", "solana", "10"))

	require.ErrorIs(t, err, ramp.ErrRequestNotSent)
}
