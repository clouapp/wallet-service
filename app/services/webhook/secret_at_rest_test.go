package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/pkg/types"
)

func expectedSignature(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestWebhook_Secret_IsSealedAtRestAndSignaturesUseThePlaintext(t *testing.T) {
	f := newScopedFixture(t)
	receiver := &recordingReceiver{status: http.StatusOK}
	server := httptest.NewServer(receiver)
	defer server.Close()
	cfg := f.insertConfig(t, server.URL, []string{withdrawalEvents}, &f.accountID, nil)

	loaded, err := f.svc.webhookConfigRepo.FindByID(context.Background(), cfg.ID)
	if err != nil || loaded == nil || loaded.Secret != scopedSecret {
		t.Fatal("the config port must hand out the plaintext secret")
	}

	if _, err := f.svc.EnqueueScoped(context.Background(), f.event(uuid.NewString())); err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	if delivered, err := f.svc.DeliverPending(context.Background(), deliveryBatchSize); err != nil || delivered != 1 {
		t.Fatalf("DeliverPending = %d, %v", delivered, err)
	}
	if receiver.signatures[0] != expectedSignature(scopedSecret, receiver.bodies[0]) {
		t.Fatal("X-Vault-Signature must stay the HMAC-SHA256 of the body under the plaintext secret")
	}
}

func TestWebhook_Config_WithAPlaintextSecretIsRefusedOnRead(t *testing.T) {
	f := newScopedFixture(t)
	receiver := &recordingReceiver{status: http.StatusOK}
	server := httptest.NewServer(receiver)
	defer server.Close()
	cfg := f.insertConfig(t, server.URL, []string{withdrawalEvents}, &f.accountID, nil)
	f.store.findByIDErr = settings.ErrNotSealed

	err := f.svc.Deliver(context.Background(), types.WebhookMessage{
		EventID:     uuid.NewString(),
		EventType:   types.EventWithdrawalBroadcast,
		Payload:     `{"id":"refused"}`,
		DeliveryURL: server.URL,
		ConfigID:    cfg.ID.String(),
		Attempt:     1,
	})
	if err == nil {
		t.Fatal("a secret the config port refuses must not be used")
	}
	if len(receiver.bodies) != 0 {
		t.Fatal("a refused secret must not be used to sign a delivery")
	}
}
