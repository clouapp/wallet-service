package ozav

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// depositAddressesResponse is GET /wallets/{id}/deposit-addresses (measured in
// sandbox Aug/Sep 2026). Ozav may return one stable address per wallet rather
// than per order — attribution then relies on amount, token, and the oldest
// awaiting_funds row for that user (collision risk documented in the design spec).
type depositAddressesResponse struct {
	Data []depositAddressEntry `json:"data"`
}

type depositAddressEntry struct {
	Address  string `json:"address"`
	Network  string `json:"network"`
	Currency string `json:"currency"`
}

type walletBalancesResponse struct {
	Data []walletBalanceEntry `json:"data"`
}

type walletBalanceEntry struct {
	Currency  string `json:"currency"`
	Chain     string `json:"chain"`
	Available string `json:"available"`
	// Live Ozav (Oct 2026) answers balances with amount, not available, and
	// without a per-chain split on that endpoint.
	Amount string `json:"amount"`
}

// OfframpFundingInstructions answers where the user sends an off-ramp's
// crypto: the deposit address of their managed wallet for the order's token
// and network.
func (a *Adapter) OfframpFundingInstructions(
	ctx context.Context, ref ramp.CustomerRef, req ramp.OfframpFundingRequest,
) (*ramp.OfframpFundingInstructions, error) {
	walletID, err := a.resolveWallet(ctx, ref.ExternalID)
	if err != nil {
		return nil, ramp.NotSent(err)
	}
	address, err := a.depositAddressFor(ctx, walletID, req.Source.PaymentRail, req.Source.Currency)
	if err != nil {
		return nil, err
	}
	amount := strings.TrimSpace(req.Source.Amount)
	if amount == "" {
		return nil, errors.New("ozav: off-ramp funding requires a source amount")
	}
	return &ramp.OfframpFundingInstructions{
		ToAddress:   address,
		PaymentRail: normalizeVocabulary(req.Source.PaymentRail),
		Currency:    strings.ToLower(quoteCurrency(req.Source.Currency)),
		Amount:      amount,
	}, nil
}

func (a *Adapter) depositAddressFor(ctx context.Context, walletID, chain, token string) (string, error) {
	var resp depositAddressesResponse
	path := fmt.Sprintf("/wallets/%s/deposit-addresses", walletID)
	if err := a.do(ctx, Request{Method: http.MethodGet, Path: path}, &resp); err != nil {
		return "", err
	}
	wantChain := normalizeVocabulary(chain)
	wantCurrency := quoteCurrency(token)
	var chainOnlyMatch string
	for _, entry := range resp.Data {
		if entry.Address == "" {
			continue
		}
		if wantChain != "" && !strings.EqualFold(entry.Network, wantChain) {
			continue
		}
		if cur := strings.TrimSpace(entry.Currency); cur != "" {
			if strings.EqualFold(cur, wantCurrency) {
				return entry.Address, nil
			}
			continue
		}
		if chainOnlyMatch == "" {
			chainOnlyMatch = entry.Address
		}
	}
	if chainOnlyMatch != "" {
		return chainOnlyMatch, nil
	}
	return "", fmt.Errorf("ozav: no deposit address for %s on %s: %w",
		wantCurrency, wantChain, ramp.ErrProviderFailed)
}

// OfframpFundingSatisfied reports whether the managed wallet's spendable
// balance covers req.Source.Amount. The caller decides what that amount has
// to include; the wallet is shared by every order of the customer.
func (a *Adapter) OfframpFundingSatisfied(
	ctx context.Context, ref ramp.CustomerRef, req ramp.OfframpFundingRequest,
) (bool, error) {
	walletID, err := a.resolveWallet(ctx, ref.ExternalID)
	if err != nil {
		return false, err
	}
	available, err := a.spendableBalance(ctx, walletID, req.Source.PaymentRail, req.Source.Currency)
	if err != nil {
		return false, err
	}
	required := strings.TrimSpace(req.Source.Amount)
	if required == "" {
		return false, errors.New("ozav: funding check requires a source amount")
	}
	const compareScale = 6
	scaledAvail, err := ramp.ScaleAmount(available, compareScale)
	if err != nil {
		return false, fmt.Errorf("ozav: parse available balance: %w", err)
	}
	scaledNeed, err := ramp.ScaleAmount(required, compareScale)
	if err != nil {
		return false, fmt.Errorf("ozav: parse required amount: %w", err)
	}
	if _, err := ramp.SubtractAmount(scaledAvail, scaledNeed, compareScale); err != nil {
		return false, nil
	}
	return true, nil
}

func (a *Adapter) spendableBalance(ctx context.Context, walletID, chain, token string) (string, error) {
	var resp walletBalancesResponse
	path := fmt.Sprintf("/wallets/%s/balances", walletID)
	if err := a.do(ctx, Request{Method: http.MethodGet, Path: path}, &resp); err != nil {
		return "", err
	}
	wantChain := normalizeVocabulary(chain)
	wantCurrency := quoteCurrency(token)
	var chainlessFallback string
	for _, entry := range resp.Data {
		avail := strings.TrimSpace(entry.Available)
		if avail == "" {
			avail = strings.TrimSpace(entry.Amount)
		}
		if avail == "" || !strings.EqualFold(entry.Currency, wantCurrency) {
			continue
		}
		entryChain := strings.TrimSpace(entry.Chain)
		if wantChain != "" && entryChain != "" && strings.EqualFold(entryChain, wantChain) {
			return avail, nil
		}
		if entryChain == "" && chainlessFallback == "" {
			chainlessFallback = avail
		}
	}
	if chainlessFallback != "" {
		return chainlessFallback, nil
	}
	return "0", nil
}
