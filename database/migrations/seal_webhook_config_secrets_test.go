package migrations_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/pkg/security"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	legacyWebhookSecret = "markets-legacy-webhook-secret"
	webhookPayload      = `{"event":"deposit.confirmed","amount":"1.5"}`
)

// legacyWebhookConfig stores a config the way the code before the sealing
// migration did: the secret in plaintext.
func legacyWebhookConfig(t *testing.T, secret string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, `INSERT INTO webhook_configs (id, url, secret, events, is_active, created_at, updated_at)
	         VALUES (?, 'https://markets.test/hook', ?, '{deposit.confirmed}', TRUE, NOW(), NOW())`, id, secret)
	return id
}

func storedWebhookSecret(t *testing.T, id uuid.UUID) string {
	return scalar[string](t, `SELECT secret FROM webhook_configs WHERE id = ?`, id)
}

// deliveredSignature sends the payload through the real delivery path and
// returns the X-Vault-Signature the receiver got.
func deliveredSignature(t *testing.T, secret string) string {
	t.Helper()
	var signature string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		signature = r.Header.Get("X-Vault-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := webhook.NewService(nil, repositories.NewWebhookConfigRepository(nil, facades.Crypt()), repositories.NewWebhookEventRepository(nil))
	require.NoError(t, svc.Deliver(context.Background(), types.WebhookMessage{
		EventID:     uuid.NewString(),
		EventType:   types.EventDepositConfirmed,
		Payload:     webhookPayload,
		DeliveryURL: server.URL,
		Secret:      secret,
		Attempt:     1,
	}))
	require.NotEmpty(t, signature)
	return signature
}

func TestSealWebhookConfigSecretsKeepsSignaturesIdentical(t *testing.T) {
	mocks.TestDB(t)
	migration := &migrations.M00000000000440SealWebhookConfigSecrets{}
	configID := legacyWebhookConfig(t, legacyWebhookSecret)
	emptySecretID := legacyWebhookConfig(t, "")
	signatureBefore := deliveredSignature(t, storedWebhookSecret(t, configID))

	require.NoError(t, migration.Up())

	sealed := storedWebhookSecret(t, configID)
	require.NotEqual(t, legacyWebhookSecret, sealed)
	require.True(t, security.IsSealedSecret(sealed))
	require.True(t, security.IsSealedSecret(storedWebhookSecret(t, emptySecretID)), "an empty secret is sealed too")

	opened, err := security.OpenSecret(facades.Crypt(), sealed)
	require.NoError(t, err)
	require.Equal(t, legacyWebhookSecret, opened)
	require.Equal(t, signatureBefore, deliveredSignature(t, opened), "Markets verifies X-Vault-Signature; it must not change")
}

func TestSealWebhookConfigSecretsIsIdempotent(t *testing.T) {
	mocks.TestDB(t)
	migration := &migrations.M00000000000440SealWebhookConfigSecrets{}
	configID := legacyWebhookConfig(t, legacyWebhookSecret)

	require.NoError(t, migration.Up())
	sealedOnce := storedWebhookSecret(t, configID)
	require.NoError(t, migration.Up())

	require.Equal(t, sealedOnce, storedWebhookSecret(t, configID), "a sealed row is not sealed twice")
}

func TestSealWebhookConfigSecretsDownRestoresPlaintext(t *testing.T) {
	mocks.TestDB(t)
	migration := &migrations.M00000000000440SealWebhookConfigSecrets{}
	configID := legacyWebhookConfig(t, legacyWebhookSecret)
	require.NoError(t, migration.Up())

	require.NoError(t, migration.Down())
	require.Equal(t, legacyWebhookSecret, storedWebhookSecret(t, configID))
	require.NoError(t, migration.Down(), "down is idempotent too")
	require.Equal(t, legacyWebhookSecret, storedWebhookSecret(t, configID))

	require.NoError(t, migration.Up())
	require.True(t, security.IsSealedSecret(storedWebhookSecret(t, configID)))
}

func TestSealWebhookConfigSecretsWidensTheColumnForLongSecrets(t *testing.T) {
	mocks.TestDB(t)
	exec(t, `ALTER TABLE webhook_configs ALTER COLUMN secret TYPE VARCHAR(255)`)
	longSecret := strings.Repeat("s", 255)
	configID := legacyWebhookConfig(t, longSecret)

	require.NoError(t, (&migrations.M00000000000440SealWebhookConfigSecrets{}).Up())

	require.Greater(t, len(storedWebhookSecret(t, configID)), 255, "the sealed form outgrows varchar(255)")
	opened, err := security.OpenSecret(facades.Crypt(), storedWebhookSecret(t, configID))
	require.NoError(t, err)
	require.Equal(t, longSecret, opened)
}
