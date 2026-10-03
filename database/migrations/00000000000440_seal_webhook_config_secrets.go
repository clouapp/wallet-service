package migrations

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/pkg/security"
)

// M00000000000440SealWebhookConfigSecrets encrypts webhook_configs.secret at
// rest with the application cipher (APP_KEY). Signatures do not change: the
// delivery path opens the secret and signs with the same plaintext.
//
// Up widens the column (a sealed value is longer than 255 characters for
// long secrets) and seals every row that is not sealed yet, so it can be
// re-run. Down opens every sealed row back to plaintext and keeps the wider
// column: narrowing it again could truncate a secret written after Up.
type M00000000000440SealWebhookConfigSecrets struct{}

type webhookSecretRow struct {
	ID     uuid.UUID
	Secret string
}

func (r *M00000000000440SealWebhookConfigSecrets) Signature() string {
	return "00000000000440_seal_webhook_config_secrets"
}

func (r *M00000000000440SealWebhookConfigSecrets) Up() error {
	if _, err := facades.Orm().Query().Exec(`ALTER TABLE webhook_configs ALTER COLUMN secret TYPE TEXT`); err != nil {
		return fmt.Errorf("widen webhook_configs.secret: %w", err)
	}
	cipher := facades.Crypt()
	return rewriteWebhookSecrets(func(stored string) (string, bool, error) {
		if security.IsSealedSecret(stored) {
			return "", false, nil
		}
		sealed, err := security.SealSecret(cipher, stored)
		return sealed, true, err
	})
}

func (r *M00000000000440SealWebhookConfigSecrets) Down() error {
	cipher := facades.Crypt()
	return rewriteWebhookSecrets(func(stored string) (string, bool, error) {
		if !security.IsSealedSecret(stored) {
			return "", false, nil
		}
		plaintext, err := security.OpenSecret(cipher, stored)
		return plaintext, true, err
	})
}

// rewriteWebhookSecrets applies transform to every row in one transaction.
// transform reports whether the row needs rewriting; each update is
// conditioned on the value it read, so a concurrent change is not clobbered.
func rewriteWebhookSecrets(transform func(stored string) (string, bool, error)) error {
	return facades.Orm().Transaction(func(tx orm.Query) error {
		var rows []webhookSecretRow
		if err := tx.Raw(`SELECT id, secret FROM webhook_configs FOR UPDATE`).Scan(&rows); err != nil {
			return fmt.Errorf("load webhook secrets: %w", err)
		}
		for _, row := range rows {
			rewritten, changed, err := transform(row.Secret)
			if err != nil {
				return fmt.Errorf("webhook config %s: %w", row.ID, err)
			}
			if !changed {
				continue
			}
			if _, err := tx.Exec(`UPDATE webhook_configs SET secret = ? WHERE id = ? AND secret = ?`, rewritten, row.ID, row.Secret); err != nil {
				return fmt.Errorf("update webhook config %s: %w", row.ID, err)
			}
		}
		return nil
	})
}
