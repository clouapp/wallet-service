package migrations

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"
)

// M00000000000440SealWebhookConfigSecrets encrypts webhook_configs.secret at rest.
type M00000000000440SealWebhookConfigSecrets struct{}

type webhookSecretRow struct {
	ID     uuid.UUID
	Secret string
}

func (r *M00000000000440SealWebhookConfigSecrets) Signature() string {
	return "00000000000440_seal_webhook_config_secrets"
}

func (r *M00000000000440SealWebhookConfigSecrets) Up() error {
	if _, err := migrationQuery().Exec(`ALTER TABLE webhook_configs ALTER COLUMN secret TYPE TEXT`); err != nil {
		return fmt.Errorf("widen webhook_configs.secret: %w", err)
	}
	cipher := migrationCipher()
	return rewriteWebhookSecrets(func(stored string) (string, bool, error) {
		if isSealedSecret(stored) {
			return "", false, nil
		}
		sealed, err := sealSecret(cipher, stored)
		return sealed, true, err
	})
}

func (r *M00000000000440SealWebhookConfigSecrets) Down() error {
	cipher := migrationCipher()
	return rewriteWebhookSecrets(func(stored string) (string, bool, error) {
		if !isSealedSecret(stored) {
			return "", false, nil
		}
		plaintext, err := openSecret(cipher, stored)
		return plaintext, true, err
	})
}

func rewriteWebhookSecrets(transform func(stored string) (string, bool, error)) error {
	return migrationTransaction(func(tx orm.Query) error {
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
