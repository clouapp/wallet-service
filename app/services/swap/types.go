// Package swap coordinates executable quotes and their account-scoped orders.
package swap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/pkg/amount"
)

const (
	ProviderSwapKit     = "swapkit"
	ProviderLiFi        = "lifi"
	StatusQuoted        = "quoted"
	StatusPreparing     = "preparing"
	StatusBroadcast     = "broadcast"
	StatusProcessing    = "processing"
	StatusReconciling   = "reconciling"
	StatusCompleted     = "completed"
	StatusRefundPending = "refund_pending"
	StatusRefunded      = "refunded"
	StatusPartial       = "partial"
	StatusFailed        = "failed"
	StatusExpired       = "expired"
)

var (
	ErrDisabled            = errors.New("swaps are disabled")
	ErrInvalidRequest      = errors.New("invalid swap request")
	ErrUnsupported         = errors.New("swap route is unsupported")
	ErrNoRoute             = errors.New("no swap route available")
	ErrProviderUnavailable = errors.New("swap provider unavailable")
	ErrProviderRejected    = errors.New("swap provider rejected the request")
	ErrInvalidQuote        = errors.New("swap provider returned an invalid quote")
	ErrNotFound            = errors.New("swap not found")
	ErrExpired             = errors.New("swap quote expired")
	ErrConflict            = errors.New("swap request conflicts with an existing order")
	ErrInProgress          = errors.New("swap is already in progress")
	ErrWalletBlocked       = errors.New("swap wallet is not active")
	ErrInvalidPassphrase   = errors.New("invalid wallet passphrase")
	ErrInvalidTOTP         = errors.New("invalid authentication code")
	ErrExecutionFailed     = errors.New("swap execution failed before funding")
	ErrUncertainSend       = errors.New("swap funding outcome is indeterminate")
)

// Asset identifies an exact asset on an exact network; symbols alone are not identities.
type Asset struct {
	Symbol    string `json:"symbol"`
	Chain     string `json:"chain"`
	NetworkID int64  `json:"network_id,omitempty"`
	Contract  string `json:"contract,omitempty"`
	Decimals  int    `json:"decimals"`
	Native    bool   `json:"native"`
	Testnet   bool   `json:"testnet,omitempty"`
}

// Same reports whether both descriptors refer to the same on-chain asset.
func (a Asset) Same(b Asset) bool {
	contractMatches := a.Contract == b.Contract
	if a.NetworkID > 0 && a.Chain != "tron" {
		contractMatches = strings.EqualFold(a.Contract, b.Contract)
	}
	return a.Chain == b.Chain && a.NetworkID == b.NetworkID && a.Native == b.Native && a.Testnet == b.Testnet &&
		(a.Native || contractMatches)
}

// SupportedSymbol limits the first release to the principal cryptocurrency group.
func SupportedSymbol(symbol string) bool {
	switch strings.ToUpper(symbol) {
	case "BTC", "ETH", "SOL", "LTC", "XRP", "BNB", "TRX", "POL", "USDT", "USDC":
		return true
	}
	return false
}

// QuoteRequest contains only public transaction parameters, never signing material.
type QuoteRequest struct {
	From        Asset  `json:"from"`
	To          Asset  `json:"to"`
	FromAddress string `json:"from_address"`
	ToAddress   string `json:"to_address"`
	AmountRaw   string `json:"amount_raw"`
	SlippageBPS int    `json:"slippage_bps"`
}

// Validate rejects malformed requests before any provider is contacted.
func (r QuoteRequest) Validate() error {
	units, ok := amount.ParseBaseUnits(r.AmountRaw)
	if !ok || units.Sign() <= 0 || len(r.AmountRaw) > 78 || r.From.Same(r.To) ||
		!SupportedSymbol(r.From.Symbol) || !SupportedSymbol(r.To.Symbol) ||
		r.FromAddress == "" || r.ToAddress == "" || r.From.Chain == "" || r.To.Chain == "" ||
		r.From.Decimals < 0 || r.From.Decimals > 36 || r.To.Decimals < 0 || r.To.Decimals > 36 ||
		r.SlippageBPS < 0 || r.SlippageBPS > 500 ||
		(!r.From.Native && r.From.Contract == "") || (!r.To.Native && r.To.Contract == "") {
		return ErrInvalidRequest
	}
	return nil
}

// ProviderConfig is opened for one call and must never be persisted or logged.
type ProviderConfig struct {
	Enabled bool
	APIKey  string
}

// RoutingConfig is the effective administrative policy for one source asset and network.
type RoutingConfig struct {
	Enabled     bool
	Preferred   string
	Fallback    bool
	SlippageBPS int
	QuoteTTL    time.Duration
	Providers   map[string]ProviderConfig
}

// Policy resolves current configuration, including per-network overrides.
type Policy interface {
	Resolve(context.Context, string, string) (RoutingConfig, error)
}

// Fee retains an exact amount and the asset in which the provider charges it.
type Fee struct {
	Name   string `json:"name"`
	Amount string `json:"amount"`
	Asset  string `json:"asset"`
}

// Attempt records quote routing without retaining provider error bodies or credentials.
type Attempt struct {
	Provider string `json:"provider"`
	Outcome  string `json:"outcome"`
}

// Quote freezes the route, minimum output and provider the customer may accept.
type Quote struct {
	ID               uuid.UUID
	Provider         string
	RouteID          string
	Request          QuoteRequest
	ExpectedRaw      string
	MinimumRaw       string
	ExpiresAt        time.Time
	EstimatedSeconds int
	Fees             []Fee
	Attempts         []Attempt
	Payload          json.RawMessage
}

// Validate checks the provider's monetary bounds before presenting a quote.
func (q *Quote) Validate(now time.Time) error {
	if q == nil {
		return ErrInvalidQuote
	}
	expected, ok := amount.ParseBaseUnits(q.ExpectedRaw)
	minimum, minOK := amount.ParseBaseUnits(q.MinimumRaw)
	if !ok || !minOK || len(q.ExpectedRaw) > 78 || len(q.MinimumRaw) > 78 || expected.Sign() <= 0 || minimum.Sign() <= 0 || minimum.Cmp(expected) > 0 ||
		q.RouteID == "" || !q.ExpiresAt.After(now) || !json.Valid(q.Payload) {
		return ErrInvalidQuote
	}
	return nil
}

// Funding is the provider's immutable funding instruction, validated before execution.
type Funding struct {
	Kind            string          `json:"kind"`
	To              string          `json:"to"`
	AmountRaw       string          `json:"amount_raw"`
	Data            string          `json:"data,omitempty"`
	Memo            string          `json:"memo,omitempty"`
	DestinationTag  *uint32         `json:"destination_tag,omitempty"`
	ApprovalAddress string          `json:"approval_address,omitempty"`
	ChainID         int64           `json:"chain_id,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

// Progress is normalized tracking data; completion still requires chain verification.
type Progress struct {
	Status           string
	ReceivingHash    string
	ReceivedRaw      string
	ReceivingAddress string
	ReceivingAsset   Asset
	RefundHash       string
	RefundRaw        string
}

// Provider is the port implemented by each external swap adapter.
type Provider interface {
	Name() string
	Quote(context.Context, QuoteRequest, ProviderConfig) (*Quote, error)
	Build(context.Context, *Quote, ProviderConfig) (*Funding, error)
	Status(context.Context, *Quote, string, ProviderConfig) (*Progress, error)
}

// Factory resolves adapters without taking responsibility for routing or execution.
type Factory struct{ providers map[string]Provider }

// NewFactory registers each distinct, known provider exactly once.
func NewFactory(providers ...Provider) (*Factory, error) {
	f := &Factory{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		if provider == nil {
			return nil, fmt.Errorf("swap factory: provider is required")
		}
		name := provider.Name()
		if (name != ProviderSwapKit && name != ProviderLiFi) || f.providers[name] != nil {
			return nil, fmt.Errorf("swap factory: invalid or duplicate provider %q", name)
		}
		f.providers[name] = provider
	}
	return f, nil
}

// Get returns the registered adapter, or an unsupported-route error.
func (f *Factory) Get(name string) (Provider, error) {
	if f == nil || f.providers[name] == nil {
		return nil, ErrUnsupported
	}
	return f.providers[name], nil
}
