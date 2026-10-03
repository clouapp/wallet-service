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
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/pkg/security"
)

func storedSecret(t *testing.T, configID uuid.UUID) string {
	t.Helper()
	var secret string
	if err := facades.Orm().Query().Raw(`SELECT secret FROM webhook_configs WHERE id = ?`, configID).Scan(&secret); err != nil {
		t.Fatalf("read stored secret: %v", err)
	}
	return secret
}

func expectedSignature(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookSecretIsSealedAtRestAndSignaturesUseThePlaintext(t *testing.T) {
	f := newScopedFixture(t)
	receiver := &recordingReceiver{status: http.StatusOK}
	server := httptest.NewServer(receiver)
	defer server.Close()
	cfg := insertOwnedConfig(t, server.URL, []string{withdrawalEvents}, &f.accountID, nil)

	stored := storedSecret(t, cfg.ID)
	if stored == scopedSecret || !security.IsSealedSecret(stored) {
		t.Fatalf("webhook_configs.secret must be sealed at rest, got %q", stored)
	}
	if opened, err := facades.Crypt().DecryptString(stored); err != nil || opened != scopedSecret {
		t.Fatalf("stored secret must open to the plaintext: %q, %v", opened, err)
	}
	loaded, err := f.svc.webhookConfigRepo.FindByID(context.Background(), cfg.ID)
	if err != nil || loaded == nil || loaded.Secret != scopedSecret {
		t.Fatalf("the repository must hand out the plaintext secret: %+v, %v", loaded, err)
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

func TestWebhookConfigWithAPlaintextSecretIsRefusedOnRead(t *testing.T) {
	f := newScopedFixture(t)
	cfg := insertOwnedConfig(t, "https://owned.test/hook", []string{withdrawalEvents}, &f.accountID, nil)
	if _, err := facades.Orm().Query().Exec(`UPDATE webhook_configs SET secret = 'written-around-the-repository' WHERE id = ?`, cfg.ID); err != nil {
		t.Fatalf("plant plaintext secret: %v", err)
	}

	if _, err := f.svc.webhookConfigRepo.FindByID(context.Background(), cfg.ID); err == nil {
		t.Fatal("a plaintext secret at rest must fail loudly, not be used")
	}
}
