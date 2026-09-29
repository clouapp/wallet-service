package webhook

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

func TestIntegrationDeliverDepositConfirmed(t *testing.T) {
	webhookURL := os.Getenv("MARKETS_WEBHOOK_URL")
	if webhookURL == "" {
		t.Skip("MARKETS_WEBHOOK_URL is empty")
	}

	secret := os.Getenv("MARKETS_WEBHOOK_SECRET")
	macroAddress := os.Getenv("MARKETS_MACRO_ADDRESS")
	bitgoAddress := os.Getenv("MARKETS_BITGO_ADDRESS")
	if secret == "" || macroAddress == "" || bitgoAddress == "" {
		t.Fatal("MARKETS_WEBHOOK_URL is set but MARKETS_WEBHOOK_SECRET, MARKETS_MACRO_ADDRESS, or MARKETS_BITGO_ADDRESS is empty")
	}

	svc := newTestWebhookSvc()
	ctx := context.Background()

	badSig := deliverDepositConfirmed(t, svc, ctx, webhookURL, "wrong-secret", macroAddress, models.SymbolUSDT, "macro-bad-signature")
	if badSig == nil {
		t.Fatal("bad X-Vault-Signature must fail delivery")
	}

	ignored := deliverDepositConfirmed(t, svc, ctx, webhookURL, secret, bitgoAddress, models.SymbolUSDT, "macro-bitgo-ignored")
	if ignored != nil {
		t.Fatalf("bitgo address delivery: %v", ignored)
	}

	credited := deliverDepositConfirmed(t, svc, ctx, webhookURL, secret, macroAddress, models.SymbolUSDT, "macro-usdt-credited")
	if credited != nil {
		t.Fatalf("macro_wallets address delivery: %v", credited)
	}
}

func deliverDepositConfirmed(t *testing.T, svc *Service, ctx context.Context, webhookURL, secret, toAddress, asset, txHash string) error {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"id":         uuid.New().String(),
		"type":       string(types.EventDepositConfirmed),
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"data": models.Transaction{
			ToAddress: toAddress,
			Asset:     asset,
			Amount:    "2.5",
			TxHash:    txHash,
			Chain:     models.ChainPolygon,
		},
	})
	if err != nil {
		t.Fatalf("marshal deposit.confirmed: %v", err)
	}

	return svc.Deliver(ctx, types.WebhookMessage{
		EventID:     uuid.New().String(),
		EventType:   types.EventDepositConfirmed,
		Payload:     string(payload),
		DeliveryURL: webhookURL,
		Secret:      secret,
		Attempt:     1,
	})
}
