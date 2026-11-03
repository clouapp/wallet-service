package swapproviders

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/httpclient"
)

// SwapKit adapts quotes, builds and tracking from the SwapKit v3 API.
type SwapKit struct{ client client }

// NewSwapKit creates an adapter with a bounded, non-redirecting HTTP transport.
func NewSwapKit(deps Deps) *SwapKit {
	return &SwapKit{client: newClient(deps, "https://api.swapkit.dev", "x-api-key")}
}

// Name identifies the adapter in administrative routing rules.
func (*SwapKit) Name() string { return swap.ProviderSwapKit }

type swapKitRoute struct {
	RouteID         string            `json:"routeId"`
	SellAsset       string            `json:"sellAsset"`
	BuyAsset        string            `json:"buyAsset"`
	SellAmount      string            `json:"sellAmount"`
	BuyAmount       string            `json:"buyAmount"`
	Minimum         string            `json:"buyAmountMaxSlippage"`
	Expected        string            `json:"expectedBuyAmount"`
	ExpectedMinimum string            `json:"expectedBuyAmountMaxSlippage"`
	Expiration      string            `json:"expiration"`
	Source          string            `json:"sourceAddress"`
	Destination     string            `json:"destinationAddress"`
	Target          string            `json:"targetAddress"`
	Inbound         string            `json:"inboundAddress"`
	Memo            string            `json:"memo"`
	TxType          string            `json:"txType"`
	Tx              json.RawMessage   `json:"tx"`
	NextActions     []json.RawMessage `json:"nextActions"`
	Fees            []struct {
		Type   string `json:"type"`
		Amount string `json:"amount"`
		Asset  string `json:"asset"`
	} `json:"fees"`
	Time struct {
		Total json.Number `json:"total"`
	} `json:"estimatedTime"`
	Meta struct {
		Approval string `json:"approvalAddress"`
	} `json:"meta"`
}

func swapKitAsset(asset swap.Asset) (string, error) {
	chains := map[string]string{"eth": "ETH", "btc": "BTC", "sol": "SOL", "ltc": "LTC", "xrp": "XRP", "bsc": "BSC", "tron": "TRON", "polygon": "POL", "base": "BASE", "arbitrum": "ARB"}
	prefix := chains[asset.Chain]
	if prefix == "" {
		return "", swap.ErrUnsupported
	}
	id := prefix + "." + strings.ToUpper(asset.Symbol)
	if !asset.Native {
		id += "-" + asset.Contract
	}
	return id, nil
}

// Quote obtains exact-input routes without requesting a signature or moving funds.
func (s *SwapKit) Quote(ctx context.Context, request swap.QuoteRequest, credential swap.ProviderConfig) (*swap.Quote, error) {
	if request.From.Testnet || request.To.Testnet {
		return nil, swap.ErrUnsupported
	}
	from, err := swapKitAsset(request.From)
	if err != nil {
		return nil, err
	}
	to, err := swapKitAsset(request.To)
	if err != nil {
		return nil, err
	}
	units, ok := amount.ParseBaseUnits(request.AmountRaw)
	if !ok {
		return nil, swap.ErrInvalidRequest
	}
	body := map[string]any{"sellAsset": from, "buyAsset": to, "sellAmount": amount.FormatBaseUnits(units, request.From.Decimals),
		"sourceAddress": request.FromAddress, "destinationAddress": request.ToAddress, "refundAddress": request.FromAddress,
		"refundAddressSetting": "STRICT", "quoteType": "EXACT_INPUT", "slippage": json.Number(fmt.Sprintf("%d.%02d", request.SlippageBPS/100, request.SlippageBPS%100))}
	var response struct {
		Routes []swapKitRoute `json:"routes"`
	}
	if err := s.client.request(ctx, httpclient.MethodPost, "/v3/quote", body, credential, &response); err != nil {
		return nil, err
	}
	if len(response.Routes) == 0 {
		return nil, swap.ErrNoRoute
	}
	for _, route := range response.Routes {
		quote, err := s.normalize(route, request)
		if err != nil {
			return nil, err
		}
		return quote, nil
	}
	return nil, swap.ErrNoRoute
}

func (s *SwapKit) normalize(route swapKitRoute, request swap.QuoteRequest) (*swap.Quote, error) {
	from, err := swapKitAsset(request.From)
	if err != nil {
		return nil, err
	}
	to, err := swapKitAsset(request.To)
	if err != nil {
		return nil, err
	}
	if route.SellAsset != from || route.BuyAsset != to {
		return nil, swap.ErrInvalidQuote
	}
	sell, err := amount.DecimalToBaseUnits(route.SellAmount, request.From.Decimals)
	if err != nil || sell.String() != request.AmountRaw {
		return nil, swap.ErrInvalidQuote
	}
	if route.Source != "" && !sameAddress(request.From, route.Source, request.FromAddress) {
		return nil, swap.ErrInvalidQuote
	}
	if route.Destination != "" && !sameAddress(request.To, route.Destination, request.ToAddress) {
		return nil, swap.ErrInvalidQuote
	}
	expected, minimum := route.Expected, route.ExpectedMinimum
	if expected == "" {
		expected = route.BuyAmount
	}
	if minimum == "" {
		minimum = route.Minimum
	}
	expectedRaw, err := amount.DecimalToBaseUnits(expected, request.To.Decimals)
	if err != nil {
		return nil, swap.ErrInvalidQuote
	}
	minimumRaw, err := amount.DecimalToBaseUnits(minimum, request.To.Decimals)
	if err != nil {
		return nil, swap.ErrInvalidQuote
	}
	expires := time.Now().UTC().Add(time.Minute)
	if route.Expiration != "" {
		expires, err = time.Parse(time.RFC3339, route.Expiration)
		if err != nil {
			seconds, parseErr := strconv.ParseInt(route.Expiration, 10, 64)
			if parseErr != nil || seconds <= 0 {
				return nil, swap.ErrInvalidQuote
			}
			expires = time.Unix(seconds, 0).UTC()
		}
	}
	payload, err := jsonPayload(route)
	if err != nil {
		return nil, err
	}
	duration := 0
	if estimate, parseErr := time.ParseDuration(route.Time.Total.String() + "s"); parseErr == nil && estimate > 0 {
		duration = int(estimate / time.Second)
		if estimate%time.Second != 0 {
			duration++
		}
	}
	quote := &swap.Quote{Provider: s.Name(), RouteID: route.RouteID, Request: request, ExpectedRaw: expectedRaw.String(), MinimumRaw: minimumRaw.String(), ExpiresAt: expires, EstimatedSeconds: duration, Payload: payload}
	for _, fee := range route.Fees {
		quote.Fees = append(quote.Fees, swap.Fee{Name: fee.Type, Amount: fee.Amount, Asset: fee.Asset})
	}
	return quote, nil
}

// Build pins the accepted route and rejects output changes below its accepted minimum.
func (s *SwapKit) Build(ctx context.Context, quote *swap.Quote, credential swap.ProviderConfig) (*swap.Funding, error) {
	if quote == nil {
		return nil, swap.ErrInvalidQuote
	}
	body := map[string]any{"routeId": quote.RouteID, "sourceAddress": quote.Request.FromAddress, "destinationAddress": quote.Request.ToAddress,
		"refundAddress": quote.Request.FromAddress, "refundAddressSetting": "STRICT", "slippageSetting": "STRICT", "quoteType": "EXACT_INPUT"}
	var route swapKitRoute
	if err := s.client.request(ctx, httpclient.MethodPost, "/v3/swap", body, credential, &route); err != nil {
		return nil, err
	}
	if route.RouteID != quote.RouteID || len(route.NextActions) > 0 {
		return nil, swap.ErrUnsupported
	}
	refreshed, err := s.normalize(route, quote.Request)
	if err != nil {
		return nil, err
	}
	minimum, ok := amount.ParseBaseUnits(quote.MinimumRaw)
	if !ok {
		return nil, swap.ErrInvalidQuote
	}
	actual, ok := amount.ParseBaseUnits(refreshed.MinimumRaw)
	if !ok || actual.Cmp(minimum) < 0 {
		return nil, swap.ErrExpired
	}
	funding := &swap.Funding{Kind: "transfer", To: route.Inbound, AmountRaw: quote.Request.AmountRaw, Memo: route.Memo, ApprovalAddress: route.Meta.Approval, Payload: route.Tx}
	if funding.To == "" {
		funding.To = route.Target
	}
	switch route.TxType {
	case "EVM":
		var transaction struct {
			To    string `json:"to"`
			From  string `json:"from"`
			Data  string `json:"data"`
			Value string `json:"value"`
		}
		if json.Unmarshal(route.Tx, &transaction) != nil || transaction.To == "" {
			return nil, swap.ErrInvalidQuote
		}
		if transaction.From != "" && !sameAddress(quote.Request.From, transaction.From, quote.Request.FromAddress) {
			return nil, swap.ErrInvalidQuote
		}
		funding.Kind = "evm"
		// EVM protocol memos are already encoded in transaction calldata.
		funding.Memo = ""
		funding.To = transaction.To
		funding.Data = transaction.Data
		funding.AmountRaw = transaction.Value
		funding.ChainID = quote.Request.From.NetworkID
	case "SERIALIZED_BASE64":
		var encoded string
		if json.Unmarshal(route.Tx, &encoded) != nil {
			return nil, swap.ErrInvalidQuote
		}
		funding.Kind = "solana"
		funding.Data = encoded
	case "RIPPLE":
		var transaction struct {
			Destination string  `json:"Destination"`
			Tag         *uint32 `json:"DestinationTag"`
		}
		if json.Unmarshal(route.Tx, &transaction) != nil {
			return nil, swap.ErrInvalidQuote
		}
		if transaction.Destination != "" {
			funding.To = transaction.Destination
		}
		funding.DestinationTag = transaction.Tag
	case "TRON":
		if err := validateTronDeposit(route.Tx, quote.Request, funding); err != nil {
			return nil, err
		}
	case "PSBT":
		if funding.To == "" {
			return nil, swap.ErrUnsupported
		}
	default:
		return nil, swap.ErrUnsupported
	}
	return funding, nil
}

// Only ordinary TRX/TRC-20 deposits can be reconstructed as custody transfers.
// A provider's arbitrary smart-contract call must never become a plain transfer
// to the same address: that could move funds without executing the swap.
func validateTronDeposit(payload json.RawMessage, request swap.QuoteRequest, funding *swap.Funding) error {
	var transaction struct {
		Raw struct {
			Memo      string `json:"data"`
			Contracts []struct {
				Type      string `json:"type"`
				Parameter struct {
					Value struct {
						Owner     string      `json:"owner_address"`
						To        string      `json:"to_address"`
						Amount    json.Number `json:"amount"`
						Contract  string      `json:"contract_address"`
						Data      string      `json:"data"`
						CallValue json.Number `json:"call_value"`
					} `json:"value"`
				} `json:"parameter"`
			} `json:"contract"`
		} `json:"raw_data"`
	}
	if json.Unmarshal(payload, &transaction) != nil || len(transaction.Raw.Contracts) != 1 || transaction.Raw.Memo != "" || funding.Memo != "" {
		return swap.ErrUnsupported
	}
	contract := transaction.Raw.Contracts[0]
	value := contract.Parameter.Value
	address := func(raw string) string {
		if addressing.IsTronAddress(raw) {
			return raw
		}
		converted, err := addressing.TronAddressFromHex(raw)
		if err != nil {
			return ""
		}
		return converted
	}
	if address(value.Owner) != request.FromAddress {
		return swap.ErrInvalidQuote
	}
	to, units := "", ""
	if request.From.Native && contract.Type == "TransferContract" {
		to, units = address(value.To), value.Amount.String()
	} else if !request.From.Native && contract.Type == "TriggerSmartContract" {
		if address(value.Contract) != request.From.Contract || (value.CallValue != "" && value.CallValue != "0") {
			return swap.ErrUnsupported
		}
		data, err := hex.DecodeString(strings.TrimPrefix(value.Data, "0x"))
		if err != nil || len(data) != 68 || hex.EncodeToString(data[:4]) != "a9059cbb" {
			return swap.ErrUnsupported
		}
		for _, padding := range data[4:16] {
			if padding != 0 {
				return swap.ErrInvalidQuote
			}
		}
		to = address("0x" + hex.EncodeToString(data[16:36]))
		units = new(big.Int).SetBytes(data[36:]).String()
	} else {
		return swap.ErrUnsupported
	}
	if to == "" || to != funding.To || units != request.AmountRaw {
		return swap.ErrInvalidQuote
	}
	return nil
}

// Status normalizes the swap outcome without treating an unknown send as a failure.
func (s *SwapKit) Status(ctx context.Context, quote *swap.Quote, hash string, credential swap.ProviderConfig) (*swap.Progress, error) {
	if quote == nil || hash == "" {
		return nil, swap.ErrInvalidRequest
	}
	chainIDs := map[string]string{"eth": "1", "polygon": "137", "bsc": "56", "base": "8453", "arbitrum": "42161", "sol": "solana", "btc": "bitcoin", "ltc": "litecoin", "tron": "728126428", "xrp": "ripple"}
	type trackedLeg struct {
		Hash      string `json:"hash"`
		Status    string `json:"status"`
		ToAsset   string `json:"toAsset"`
		ToAmount  string `json:"toAmount"`
		ToAddress string `json:"toAddress"`
	}
	var result struct {
		Status   string       `json:"status"`
		Tracking string       `json:"trackingStatus"`
		Legs     []trackedLeg `json:"legs"`
	}
	if err := s.client.request(ctx, httpclient.MethodPost, "/track", map[string]string{"hash": hash, "chainId": chainIDs[quote.Request.From.Chain], "routeId": quote.RouteID}, credential, &result); err != nil {
		return nil, err
	}
	progress := &swap.Progress{Status: swap.StatusProcessing}
	switch result.Status {
	case "completed":
		progress.Status = swap.StatusCompleted
	case "refunded":
		progress.Status = swap.StatusRefunded
	case "failed", "unknown":
		progress.Status = swap.StatusReconciling
	}
	if result.Tracking == "partially_refunded" {
		progress.Status = swap.StatusPartial
	}
	if len(result.Legs) > 0 {
		leg := result.Legs[len(result.Legs)-1]
		progress.ReceivingHash = leg.Hash
		progress.ReceivingAddress = leg.ToAddress
		expectedAsset, _ := swapKitAsset(quote.Request.To)
		if leg.ToAsset == expectedAsset {
			units, err := amount.DecimalToBaseUnits(leg.ToAmount, quote.Request.To.Decimals)
			if err == nil {
				progress.ReceivedRaw = units.String()
				progress.ReceivingAsset = quote.Request.To
			}
		}
		if progress.Status == swap.StatusRefunded {
			progress.RefundHash = leg.Hash
			refundAsset, _ := swapKitAsset(quote.Request.From)
			if leg.ToAsset == refundAsset {
				units, err := amount.DecimalToBaseUnits(leg.ToAmount, quote.Request.From.Decimals)
				if err == nil {
					progress.RefundRaw = units.String()
				}
			}
		}
	}
	return progress, nil
}
