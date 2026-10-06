package migrations

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type mismatchCipher struct{}

func (mismatchCipher) EncryptString(value string) (string, error) {
	return "cipher:" + value, nil
}

func (mismatchCipher) DecryptString(string) (string, error) {
	return "different", nil
}

func TestSealWebhookSecretAbortsWhenTheCopyDoesNotMatch(t *testing.T) {
	_, changed, err := sealWebhookSecret(mismatchCipher{}, "plaintext-value")
	if err == nil || changed {
		t.Fatal("a seal that does not open to the stored secret must abort")
	}
}

func TestWebhookSecretRewriteRollsBackWhenALaterRowFails(t *testing.T) {
	fixtures.TestDB(t)
	const first = "rollback-webhook-secret-a"
	const second = "rollback-webhook-secret-b"
	firstID := insertPlainWebhook(t, first)
	secondID := insertPlainWebhook(t, second)

	err := rewriteWebhookSecretColumn(func(stored string) (string, bool, error) {
		if stored == first {
			sealed, sealErr := settings.Seal(facades.Crypt(), stored)
			return sealed, true, sealErr
		}
		return "", false, errors.New("seal mismatch")
	})
	require.Error(t, err)
	if readWebhookSecret(t, firstID) != first || readWebhookSecret(t, secondID) != second {
		t.Fatal("aborted seal cleared a stored webhook secret")
	}
}

func insertPlainWebhook(t *testing.T, secret string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO webhook_configs (id, url, secret, events, is_active, created_at, updated_at)
		 VALUES (?, 'https://markets.test/hook', ?, '{deposit.confirmed}', TRUE, NOW(), NOW())`,
		id, secret,
	)
	require.NoError(t, err)
	return id
}

func readWebhookSecret(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var stored string
	require.NoError(t, facades.Orm().Query().Raw(`SELECT secret FROM webhook_configs WHERE id = ?`, id).Scan(&stored))
	return stored
}
