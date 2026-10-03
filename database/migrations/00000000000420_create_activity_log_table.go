package migrations

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/pkg/security"
)

// M00000000000420CreateActivityLogTable is the slotkit activity trail.
// scope ” is the platform; account:{uuid} is one account. causer_type is the
// audience that acted. properties is a redacted before/after document: the
// column allowlist never selects a secret, a token hash, a spending limit, or
// an amount.
type M00000000000420CreateActivityLogTable struct{}

func (r *M00000000000420CreateActivityLogTable) Signature() string {
	return "00000000000420_create_activity_log_table"
}

func (r *M00000000000420CreateActivityLogTable) Up() error {
	stmts := []string{
		`CREATE TABLE activity_log (
			id            BIGSERIAL    NOT NULL,
			log_name      TEXT         NOT NULL DEFAULT 'default',
			scope         TEXT         NOT NULL DEFAULT '',
			event         TEXT         NOT NULL DEFAULT '',
			description   TEXT         NOT NULL DEFAULT '',
			subject_type  TEXT         NOT NULL DEFAULT '',
			subject_id    TEXT         NOT NULL DEFAULT '',
			causer_type   TEXT         NOT NULL DEFAULT '',
			causer_id     TEXT         NOT NULL DEFAULT '',
			causer_label  TEXT         NOT NULL DEFAULT '',
			properties    JSONB        NOT NULL DEFAULT '{}',
			batch_uuid    TEXT         NOT NULL DEFAULT '',
			created_at    TIMESTAMP(6) WITH TIME ZONE NOT NULL DEFAULT NOW(),
			CONSTRAINT activity_log_pkey PRIMARY KEY (id),
			CONSTRAINT activity_log_causer_type_known CHECK (
				causer_type IN ('', 'users', 'platform_admins', 'api_tokens', 'cli')
			),
			CONSTRAINT activity_log_properties_object CHECK (jsonb_typeof(properties) = 'object')
		)`,
		`CREATE INDEX activity_log_scope_created_idx ON activity_log (scope, created_at DESC)`,
		`CREATE INDEX activity_log_subject_idx ON activity_log (subject_type, subject_id, created_at DESC)`,
		`CREATE INDEX activity_log_causer_idx ON activity_log (causer_type, causer_id, created_at DESC)`,
		`CREATE INDEX activity_log_prune_idx ON activity_log (created_at)`,
	}
	for _, statement := range stmts {
		if _, err := facades.Orm().Query().Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000420CreateActivityLogTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS activity_log`)
	return err
}

// M00000000000430UniqueDepositPerTransaction makes the database, not a read-then-insert
// check, guarantee one deposit row per (chain, tx_hash, log_index).
type M00000000000430UniqueDepositPerTransaction struct{}

const uniqueDepositIndex = "transactions_deposit_chain_tx_log_unique"

func (r *M00000000000430UniqueDepositPerTransaction) Signature() string {
	return "00000000000430_unique_deposit_per_transaction"
}

func (r *M00000000000430UniqueDepositPerTransaction) Up() error {
	var duplicates []struct{ DuplicateGroups int64 }
	if err := facades.Orm().Query().Raw(`
		SELECT COUNT(*) AS duplicate_groups FROM (
			SELECT 1 FROM transactions
			WHERE tx_type = 'deposit'
			GROUP BY chain, tx_hash, log_index
			HAVING COUNT(*) > 1
		) duplicated`).Scan(&duplicates); err != nil {
		return fmt.Errorf("count duplicate deposits: %w", err)
	}
	if len(duplicates) == 1 && duplicates[0].DuplicateGroups > 0 {
		return fmt.Errorf("%d (chain, tx_hash, log_index) deposit groups have more than one row; resolve them before adding %s", duplicates[0].DuplicateGroups, uniqueDepositIndex)
	}
	_, err := facades.Orm().Query().Exec(`CREATE UNIQUE INDEX IF NOT EXISTS ` + uniqueDepositIndex +
		` ON transactions (chain, tx_hash, log_index) WHERE tx_type = 'deposit'`)
	return err
}

func (r *M00000000000430UniqueDepositPerTransaction) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP INDEX IF EXISTS ` + uniqueDepositIndex)
	return err
}

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

type M00000000000450AddTotpLastUsedCounterToUsers struct{}

func (r *M00000000000450AddTotpLastUsedCounterToUsers) Signature() string {
	return "00000000000450_add_totp_last_used_counter_to_users"
}

func (r *M00000000000450AddTotpLastUsedCounterToUsers) Up() error {
	_, err := facades.Orm().Query().Exec(
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_last_used_counter BIGINT NOT NULL DEFAULT 0`,
	)
	return err
}

func (r *M00000000000450AddTotpLastUsedCounterToUsers) Down() error {
	_, err := facades.Orm().Query().Exec(`ALTER TABLE users DROP COLUMN IF EXISTS totp_last_used_counter`)
	return err
}

type M00000000000460AddSessionsRevokedAtToUsers struct{}

func (r *M00000000000460AddSessionsRevokedAtToUsers) Signature() string {
	return "00000000000460_add_sessions_revoked_at_to_users"
}

func (r *M00000000000460AddSessionsRevokedAtToUsers) Up() error {
	_, err := facades.Orm().Query().Exec(
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS sessions_revoked_at TIMESTAMPTZ`,
	)
	return err
}

func (r *M00000000000460AddSessionsRevokedAtToUsers) Down() error {
	_, err := facades.Orm().Query().Exec(`ALTER TABLE users DROP COLUMN IF EXISTS sessions_revoked_at`)
	return err
}

const accountUsersRoleCheck = "account_users_role_check"

type M00000000000470AccountUsersRoleCheck struct{}

func (r *M00000000000470AccountUsersRoleCheck) Signature() string {
	return "00000000000470_account_users_role_check"
}

func (r *M00000000000470AccountUsersRoleCheck) Up() error {
	if _, err := facades.Orm().Query().Exec(`UPDATE account_users SET role = 'auditor' WHERE role = 'viewer'`); err != nil {
		return err
	}
	_, err := facades.Orm().Query().Exec(
		`ALTER TABLE account_users ADD CONSTRAINT ` + accountUsersRoleCheck +
			` CHECK (role IN ('owner', 'admin', 'auditor', 'user'))`,
	)
	return err
}

func (r *M00000000000470AccountUsersRoleCheck) Down() error {
	_, err := facades.Orm().Query().Exec(`ALTER TABLE account_users DROP CONSTRAINT IF EXISTS ` + accountUsersRoleCheck)
	return err
}

type M00000000000480CreateAccountInvitesTable struct{}

func (r *M00000000000480CreateAccountInvitesTable) Signature() string {
	return "00000000000480_create_account_invites_table"
}

func (r *M00000000000480CreateAccountInvitesTable) Up() error {
	stmts := []string{
		`CREATE TABLE account_invites (
			id          UUID         NOT NULL,
			account_id  UUID         NOT NULL,
			email       VARCHAR(255) NOT NULL,
			role        VARCHAR(20)  NOT NULL,
			token_hash  TEXT         NOT NULL,
			invited_by  UUID,
			expires_at  TIMESTAMPTZ  NOT NULL,
			accepted_at TIMESTAMPTZ,
			revoked_at  TIMESTAMPTZ,
			created_at  TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at  TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT account_invites_pkey PRIMARY KEY (id),
			CONSTRAINT account_invites_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
			CONSTRAINT account_invites_invited_by_fkey FOREIGN KEY (invited_by) REFERENCES users(id),
			CONSTRAINT account_invites_role_check CHECK (role IN ('owner', 'admin', 'auditor', 'user'))
		)`,
		`CREATE UNIQUE INDEX account_invites_pending_email ON account_invites (account_id, lower(email)) WHERE accepted_at IS NULL AND revoked_at IS NULL`,
		`CREATE INDEX account_invites_token_hash ON account_invites (token_hash)`,
	}
	for _, statement := range stmts {
		if _, err := facades.Orm().Query().Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000480CreateAccountInvitesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS account_invites`)
	return err
}
