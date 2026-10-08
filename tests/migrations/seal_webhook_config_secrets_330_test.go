package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/pkg/security"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func TestAppendix_B330_SealsPlaintextAndDoesNotSealTwice(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000540SealWebhookConfigSecrets{}
	configID := legacyWebhookConfig(t, legacyWebhookSecret)
	emptyID := legacyWebhookConfig(t, "")
	signatureBefore := deliveredSignature(t, legacyWebhookSecret)

	require.NoError(t, migration.Up())

	stored := storedWebhookSecret(t, configID)
	if stored == legacyWebhookSecret || !settings.IsSealed(stored) {
		t.Fatal("legacy plaintext webhook secret was not sealed")
	}
	if storedWebhookSecret(t, emptyID) != "" {
		t.Fatal("empty webhook secret was replaced")
	}

	loaded, err := repositories.NewWebhookConfigRepository(nil, facades.Crypt()).FindByID(context.Background(), configID)
	require.NoError(t, err)
	if loaded == nil || loaded.Secret != legacyWebhookSecret {
		t.Fatal("delivery did not open the sealed webhook secret")
	}
	if deliveredSignature(t, loaded.Secret) != signatureBefore {
		t.Fatal("webhook signature changed after sealing")
	}

	require.NoError(t, migration.Up())
	if storedWebhookSecret(t, configID) != stored {
		t.Fatal("a second migrate sealed the webhook secret again")
	}
}

func TestAppendix_B330_PrefixesACryptEnvelopeWithoutEncryptingAgain(t *testing.T) {
	fixtures.TestDB(t)
	envelope, err := security.SealSecret(facades.Crypt(), legacyWebhookSecret)
	require.NoError(t, err)
	configID := legacyWebhookConfig(t, envelope)

	migration := &migrations.M00000000000540SealWebhookConfigSecrets{}
	require.NoError(t, migration.Up())

	stored := storedWebhookSecret(t, configID)
	if stored != "enc:v1:"+envelope {
		t.Fatal("crypt envelope was encrypted again instead of tagged")
	}
	require.NoError(t, migration.Up())
	if storedWebhookSecret(t, configID) != stored {
		t.Fatal("a second migrate sealed the webhook secret again")
	}

	loaded, err := repositories.NewWebhookConfigRepository(nil, facades.Crypt()).FindByID(context.Background(), configID)
	require.NoError(t, err)
	if loaded == nil || loaded.Secret != legacyWebhookSecret {
		t.Fatal("delivery did not open the tagged webhook secret")
	}
}

func TestAppendix_B330_StoresANewSecretWithTheSealPrefix(t *testing.T) {
	fixtures.TestDB(t)
	repo := repositories.NewWebhookConfigRepository(nil, facades.Crypt())
	const plain = "new-webhook-signing-secret"
	id := uuid.New()
	require.NoError(t, repo.Create(context.Background(), &models.WebhookConfig{
		ID: id, URL: "https://markets.test/hook", Secret: plain,
		Events: "{deposit.confirmed}", IsActive: true,
	}))

	stored := storedWebhookSecret(t, id)
	if stored == plain || !settings.IsSealed(stored) {
		t.Fatal("new webhook secret is not stored with the seal prefix")
	}
	loaded, err := repo.FindByID(context.Background(), id)
	require.NoError(t, err)
	if loaded == nil || loaded.Secret != plain {
		t.Fatal("new webhook secret did not open")
	}
}
