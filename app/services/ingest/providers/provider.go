package providers

import (
	"context"
	"math/big"
	"net/http"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

type InboundTransfer struct {
	TxHash      string
	BlockNumber uint64
	BlockHash   string
	From        string
	To          string
	Amount      *big.Int
	Asset       string
	Token       *types.Token
	LogIndex    int
	Timestamp   time.Time
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
	SigningSecret      string
}

type WebhookProvider interface {
	ProviderName() string
	CreateWebhook(ctx context.Context, cfg ProviderConfig) (*ProviderWebhook, error)
	SyncAddresses(ctx context.Context, webhookID string, allAddresses []string) error
	DeleteWebhook(ctx context.Context, webhookID string) error
	VerifyInbound(headers http.Header, body []byte, secret string) (bool, error)
	ParsePayload(body []byte) ([]InboundTransfer, error)
}
