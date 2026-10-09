package migrations

import (
	"errors"
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/database/orm"
)

// sealedWebhookPrefix is the marker Appendix B item 330 stores on
// webhook_configs.secret. It is the same prefix settings.Seal writes.
const sealedWebhookPrefix = "enc:v1:"

// M00000000000540SealWebhookConfigSecrets seals webhook_configs.secret at rest
// (Appendix B, 330). Delivery opens the seal. An empty secret stays empty. A
// value that already carries enc:v1: is left unchanged. A Crypt envelope
// written before the marker is tagged, not encrypted again. Plaintext is
// replaced only after the sealed copy opens to the same bytes; a mismatch
// aborts the transaction and the old value stays.
type M00000000000540SealWebhookConfigSecrets struct{}

func (r *M00000000000540SealWebhookConfigSecrets) Signature() string {
	return "00000000000540_seal_webhook_config_secrets"
}

func (r *M00000000000540SealWebhookConfigSecrets) Up() error {
	if _, err := migrationQuery().Exec(`ALTER TABLE webhook_configs ALTER COLUMN secret TYPE TEXT`); err != nil {
		return fmt.Errorf("widen webhook_configs.secret: %w", err)
	}
	cipher := migrationCipher()
	if cipher == nil {
		return errors.New("seal webhook secret: crypt is not available")
	}
	return rewriteWebhookSecretColumn(func(stored string) (string, bool, error) {
		return sealWebhookSecret(cipher, stored)
	})
}

func (r *M00000000000540SealWebhookConfigSecrets) Down() error {
	cipher := migrationCipher()
	if cipher == nil {
		return errors.New("open webhook secret: crypt is not available")
	}
	return rewriteWebhookSecretColumn(func(stored string) (string, bool, error) {
		return unsealWebhookSecret(cipher, stored)
	})
}

// sealWebhookSecret returns the stored form for one secret. changed is false
// when the row must be left as it is. The returned error never includes the
// secret or its ciphertext.
func sealWebhookSecret(cipher sealCipher, stored string) (string, bool, error) {
	if cipher == nil {
		return "", false, errors.New("seal webhook secret: cipher is required")
	}
	if stored == "" || isSealedSetting(stored) {
		return "", false, nil
	}
	if isSealedSecret(stored) {
		return tagSealedWebhookSecret(cipher, stored)
	}
	sealed, err := sealSetting(cipher, stored)
	if err != nil {
		return "", false, errors.New("seal webhook secret: encryption failed")
	}
	if err := webhookSealMatches(cipher, sealed, stored); err != nil {
		return "", false, err
	}
	return sealed, true, nil
}

func tagSealedWebhookSecret(cipher sealCipher, stored string) (string, bool, error) {
	opened, err := openSecret(cipher, stored)
	if err != nil {
		return "", false, errors.New("seal webhook secret: stored envelope does not open")
	}
	tagged := prefixSeal(stored)
	if err := webhookSealMatches(cipher, tagged, opened); err != nil {
		return "", false, err
	}
	return tagged, true, nil
}

func webhookSealMatches(cipher sealCipher, sealed, plaintext string) error {
	opened, err := openSetting(cipher, sealed)
	if err != nil {
		return errors.New("seal webhook secret: sealed copy does not open")
	}
	if opened != plaintext {
		return errors.New("seal webhook secret: sealed copy does not match")
	}
	return nil
}

func unsealWebhookSecret(cipher sealCipher, stored string) (string, bool, error) {
	if cipher == nil {
		return "", false, errors.New("open webhook secret: cipher is required")
	}
	if stored == "" || !isSealedSetting(stored) {
		return "", false, nil
	}
	raw, ok := strings.CutPrefix(stored, sealedWebhookPrefix)
	if !ok || raw == "" || !isSealedSecret(raw) {
		return "", false, errors.New("open webhook secret: sealed value is not a crypt envelope")
	}
	opened, err := openSetting(cipher, stored)
	if err != nil {
		return "", false, errors.New("open webhook secret: sealed copy does not open")
	}
	legacy, err := openSecret(cipher, raw)
	if err != nil || legacy != opened {
		return "", false, errors.New("open webhook secret: sealed copy does not match")
	}
	return raw, true, nil
}

func rewriteWebhookSecretColumn(transform func(stored string) (string, bool, error)) error {
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
			result, err := tx.Exec(
				`UPDATE webhook_configs SET secret = ? WHERE id = ? AND secret = ?`,
				rewritten, row.ID, row.Secret,
			)
			if err != nil {
				return fmt.Errorf("update webhook config %s: %w", row.ID, err)
			}
			if result == nil || result.RowsAffected != 1 {
				return fmt.Errorf("webhook config %s secret was not updated", row.ID)
			}
		}
		return nil
	})
}
