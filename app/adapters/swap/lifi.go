package swapproviders

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/httpclient"
)

// LiFi adapts executable quotes and status checks from LI.FI.
type LiFi struct{ client client }

// NewLiFi creates an adapter whose credentials are supplied at call time.
func NewLiFi(deps Deps) *LiFi {
	return &LiFi{client: newClient(deps, "https://li.quest/v1", "x-lifi-api-key")}
}

// Name identifies the adapter in persisted routing preferences.
func (*LiFi) Name() string { return swap.ProviderLiFi }

type lifiToken struct {
	Address  string `json:"address"`
	Symbol   string `json:"symbol"`
	Decimals int    `json:"decimals"`
	ChainID  int64  `json:"chainId"`
}
type lifiChain struct {
	ID     int64     `json:"id"`
	Key    string    `json:"key"`
	Native lifiToken `json:"nativeToken"`
}
type lifiCost struct {
	Name   string    `json:"name"`
	Amount string    `json:"amount"`
	Token  lifiToken `json:"token"`
}
type lifiStep struct {
	ID     string `json:"id"`
	Tool   string `json:"tool"`
	Action struct {
		FromChain int64     `json:"fromChainId"`
		ToChain   int64     `json:"toChainId"`
		FromToken lifiToken `json:"fromToken"`
		ToToken   lifiToken `json:"toToken"`
		Amount    string    `json:"fromAmount"`
		From      string    `json:"fromAddress"`
		To        string    `json:"toAddress"`
	} `json:"action"`
	Estimate struct {
		Expected string      `json:"toAmount"`
		Minimum  string      `json:"toAmountMin"`
		Approval string      `json:"approvalAddress"`
		Duration json.Number `json:"executionDuration"`
		Fees     []lifiCost  `json:"feeCosts"`
		Gas      []lifiCost  `json:"gasCosts"`
	} `json:"estimate"`
	Transaction json.RawMessage `json:"transactionRequest"`
}

func (l *LiFi) chain(ctx context.Context, asset swap.Asset, credential swap.ProviderConfig) (*lifiChain, error) {
	var catalog struct {
		Chains []lifiChain `json:"chains"`
	}
	if err := l.client.request(ctx, httpclient.MethodGet, "/chains?chainTypes=EVM,SVM,UTXO,TVM", nil, credential, &catalog); err != nil {
		return nil, err
	}
	aliases := map[string]string{"btc": "btc", "ltc": "ltc", "sol": "sol", "tron": "tron", "xrp": "xrp"}
	for _, candidate := range catalog.Chains {
		if asset.NetworkID > 0 && candidate.ID == asset.NetworkID {
			return &candidate, nil
		}
		if asset.NetworkID == 0 && aliases[asset.Chain] != "" && candidate.Key == aliases[asset.Chain] {
			return &candidate, nil
		}
	}
	return nil, swap.ErrUnsupported
}

// Quote asks LI.FI for an exact-input route tied to both custody addresses.
func (l *LiFi) Quote(ctx context.Context, request swap.QuoteRequest, credential swap.ProviderConfig) (*swap.Quote, error) {
	if request.From.Testnet || request.To.Testnet {
		return nil, swap.ErrUnsupported
	}
	// Custody funds LI.FI routes only from EVM networks (a Diamond call) and
	// Solana (a serialized transaction); any other source could be quoted but
	// never executed, so it is left to the fallback provider.
	if !lifiExecutableSource(request.From) {
		return nil, swap.ErrUnsupported
	}
	fromChain, err := l.chain(ctx, request.From, credential)
	if err != nil {
		return nil, err
	}
	toChain, err := l.chain(ctx, request.To, credential)
	if err != nil {
		return nil, err
	}
	fromToken, toToken := request.From.Contract, request.To.Contract
	if request.From.Native {
		fromToken = fromChain.Native.Address
	}
	if request.To.Native {
		toToken = toChain.Native.Address
	}
	if fromToken == "" || toToken == "" {
		return nil, swap.ErrUnsupported
	}
	params := url.Values{
		"fromChain": {strconv.FormatInt(fromChain.ID, 10)}, "toChain": {strconv.FormatInt(toChain.ID, 10)},
		"fromToken": {fromToken}, "toToken": {toToken}, "fromAmount": {request.AmountRaw},
		"fromAddress": {request.FromAddress}, "toAddress": {request.ToAddress},
		"slippage":   {fmt.Sprintf("%d.%04d", request.SlippageBPS/10000, request.SlippageBPS%10000)},
		"integrator": {"macro-wallets"}, "order": {"RECOMMENDED"}, "allowDestinationCall": {"false"},
	}
	var step lifiStep
	if err := l.client.request(ctx, httpclient.MethodGet, "/quote?"+params.Encode(), nil, credential, &step); err != nil {
		return nil, err
	}
	if step.Action.FromChain != fromChain.ID || step.Action.ToChain != toChain.ID || step.Action.Amount != request.AmountRaw ||
		!sameAddress(request.From, step.Action.From, request.FromAddress) || !sameAddress(request.To, step.Action.To, request.ToAddress) ||
		!sameAddress(request.From, step.Action.FromToken.Address, fromToken) || !sameAddress(request.To, step.Action.ToToken.Address, toToken) ||
		step.Action.FromToken.Decimals != request.From.Decimals || step.Action.ToToken.Decimals != request.To.Decimals {
		return nil, swap.ErrInvalidQuote
	}
	payload, err := jsonPayload(step)
	if err != nil {
		return nil, err
	}
	duration, _ := strconv.Atoi(step.Estimate.Duration.String())
	quote := &swap.Quote{Provider: l.Name(), RouteID: step.ID, Request: request, ExpectedRaw: step.Estimate.Expected,
		MinimumRaw: step.Estimate.Minimum, ExpiresAt: time.Now().UTC().Add(time.Minute), EstimatedSeconds: duration, Payload: payload}
	for _, fee := range step.Estimate.Fees {
		normalized, err := normalizeLiFiCost(fee, "provider")
		if err != nil {
			return nil, err
		}
		quote.Fees = append(quote.Fees, normalized)
	}
	for _, fee := range step.Estimate.Gas {
		normalized, err := normalizeLiFiCost(fee, "network")
		if err != nil {
			return nil, err
		}
		quote.Fees = append(quote.Fees, normalized)
	}
	return quote, nil
}

func normalizeLiFiCost(cost lifiCost, name string) (swap.Fee, error) {
	units, ok := amount.ParseBaseUnits(cost.Amount)
	if !ok || units.Sign() < 0 || len(cost.Amount) > 78 || cost.Token.Decimals < 0 || cost.Token.Decimals > 36 {
		return swap.Fee{}, swap.ErrInvalidQuote
	}
	if cost.Name != "" {
		name = cost.Name
	}
	return swap.Fee{Name: name, Amount: amount.FormatBaseUnits(units, cost.Token.Decimals), Asset: cost.Token.Symbol}, nil
}

func lifiExecutableSource(asset swap.Asset) bool {
	return asset.Chain == "sol" || (asset.NetworkID > 0 && asset.Chain != "tron")
}

// Build returns the frozen quote's transaction; it never requests another route.
func (l *LiFi) Build(_ context.Context, quote *swap.Quote, _ swap.ProviderConfig) (*swap.Funding, error) {
	var step lifiStep
	if quote == nil || json.Unmarshal(quote.Payload, &step) != nil || step.ID != quote.RouteID {
		return nil, swap.ErrInvalidQuote
	}
	var transaction struct {
		To      string `json:"to"`
		From    string `json:"from"`
		Value   string `json:"value"`
		Data    string `json:"data"`
		ChainID int64  `json:"chainId"`
	}
	if json.Unmarshal(step.Transaction, &transaction) != nil {
		return nil, swap.ErrUnsupported
	}
	if transaction.From != "" && !sameAddress(quote.Request.From, transaction.From, quote.Request.FromAddress) {
		return nil, swap.ErrInvalidQuote
	}
	if quote.Request.From.NetworkID > 0 && transaction.ChainID != quote.Request.From.NetworkID {
		return nil, swap.ErrInvalidQuote
	}
	kind := "evm"
	if quote.Request.From.Chain == "sol" {
		kind = "solana"
	}
	return &swap.Funding{Kind: kind, To: transaction.To, AmountRaw: transaction.Value, Data: transaction.Data, ChainID: transaction.ChainID, ApprovalAddress: step.Estimate.Approval, Payload: step.Transaction}, nil
}

// Status tracks only the provider and route recorded on the accepted order.
func (l *LiFi) Status(ctx context.Context, quote *swap.Quote, hash string, credential swap.ProviderConfig) (*swap.Progress, error) {
	var step lifiStep
	if quote == nil || json.Unmarshal(quote.Payload, &step) != nil || hash == "" {
		return nil, swap.ErrInvalidRequest
	}
	params := url.Values{"txHash": {hash}, "fromChain": {strconv.FormatInt(step.Action.FromChain, 10)}, "toChain": {strconv.FormatInt(step.Action.ToChain, 10)}, "bridge": {step.Tool}}
	var status struct {
		Status    string `json:"status"`
		Substatus string `json:"substatus"`
		Receiving struct {
			Hash    string    `json:"txHash"`
			Amount  string    `json:"amount"`
			Address string    `json:"toAddress"`
			Token   lifiToken `json:"token"`
		} `json:"receiving"`
	}
	if err := l.client.request(ctx, httpclient.MethodGet, "/status?"+params.Encode(), nil, credential, &status); err != nil {
		return nil, err
	}
	progress := &swap.Progress{Status: swap.StatusProcessing}
	switch status.Substatus {
	case "COMPLETED":
		if status.Status == "DONE" {
			progress.Status = swap.StatusCompleted
		}
	case "REFUNDED":
		progress.Status = swap.StatusRefunded
		progress.RefundHash = status.Receiving.Hash
		progress.RefundRaw = status.Receiving.Amount
	case "PARTIAL":
		// Delivered in another token than quoted: not the quoted output, so it is
		// never credited as completed; the order reconciles for review.
		if status.Status == "DONE" {
			progress.Status = swap.StatusPartial
		}
	case "REFUND_IN_PROGRESS", "NOT_PROCESSABLE_REFUND_NEEDED":
		progress.Status = swap.StatusRefundPending
	case "UNKNOWN_ERROR":
		progress.Status = swap.StatusReconciling
	}
	if status.Status == "FAILED" && progress.Status == swap.StatusProcessing {
		progress.Status = swap.StatusReconciling
	}
	progress.ReceivingHash = status.Receiving.Hash
	progress.ReceivedRaw = status.Receiving.Amount
	progress.ReceivingAddress = status.Receiving.Address
	if status.Receiving.Token.ChainID == step.Action.ToChain && sameAddress(quote.Request.To, status.Receiving.Token.Address, step.Action.ToToken.Address) && status.Receiving.Token.Decimals == quote.Request.To.Decimals {
		progress.ReceivingAsset = quote.Request.To
	}
	return progress, nil
}

func sameAddress(asset swap.Asset, a, b string) bool {
	if asset.NetworkID > 0 && asset.Chain != "tron" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
