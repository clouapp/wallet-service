package providers

import (
	"context"
	"math/big"
	"net/textproto"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

type InboundTransfer struct {
	TxHash        string
	BlockNumber   uint64
	BlockHash     string
	From          string
	To            string
	Amount        *big.Int
	AmountIsHuman bool
	HumanAmount   string
	Asset         string
	Token         *types.Token
	LogIndex      int
	Timestamp     time.Time
}

type ProviderConfig struct {
	ChainID    string
	Network    string
	WebhookURL string
	Addresses  []string
	APIKey     string
	AuthSecret string
}

type ProviderWebhook struct {
	ProviderWebhookID string
	SigningSecret     string
}

// Header is the inbound webhook header map. Names match case-insensitively.
type Header map[string][]string

// Get returns the first value for key, or empty when absent.
func (h Header) Get(key string) string {
	if len(h) == 0 {
		return ""
	}
	return textproto.MIMEHeader(h).Get(key)
}

// Set replaces the values for key.
func (h Header) Set(key, value string) {
	textproto.MIMEHeader(h).Set(key, value)
}

type WebhookProvider interface {
	ProviderName() string
	CreateWebhook(ctx context.Context, cfg ProviderConfig) (*ProviderWebhook, error)
	SyncAddresses(ctx context.Context, webhookID string, allAddresses []string) error
	DeleteWebhook(ctx context.Context, webhookID string) error
	VerifyInbound(headers Header, body []byte, secret string) (bool, error)
	ParsePayload(body []byte) ([]InboundTransfer, error)
}
