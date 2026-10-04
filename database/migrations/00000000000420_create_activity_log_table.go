package migrations

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/settings"
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

// M00000000000490AddUsersSuspendedAt adds the platform suspension columns.
// sessions_revoked_at already exists on this branch, so this migration does
// not add it again. suspension_reason stays empty unless a later change
// decides a body for it.
type M00000000000490AddUsersSuspendedAt struct{}

func (r *M00000000000490AddUsersSuspendedAt) Signature() string {
	return "00000000000490_add_users_suspended_at"
}

func (r *M00000000000490AddUsersSuspendedAt) Up() error {
	_, err := facades.Orm().Query().Exec(
		`ALTER TABLE users
			ADD COLUMN IF NOT EXISTS suspended_at TIMESTAMPTZ,
			ADD COLUMN IF NOT EXISTS suspension_reason TEXT`,
	)
	return err
}

func (r *M00000000000490AddUsersSuspendedAt) Down() error {
	_, err := facades.Orm().Query().Exec(
		`ALTER TABLE users
			DROP COLUMN IF EXISTS suspension_reason,
			DROP COLUMN IF EXISTS suspended_at`,
	)
	return err
}

// M00000000000500CreateMfaCredentialsTable is the shared TOTP secret store
// from S3.4.5: subject_type is users or platform_admins, and last_used_counter
// is the replay step.
type M00000000000500CreateMfaCredentialsTable struct{}

func (r *M00000000000500CreateMfaCredentialsTable) Signature() string {
	return "00000000000500_create_mfa_credentials_table"
}

func (r *M00000000000500CreateMfaCredentialsTable) Up() error {
	_, err := facades.Orm().Query().Exec(`
		CREATE TABLE IF NOT EXISTS mfa_credentials (
			id                 UUID         NOT NULL,
			subject_type       VARCHAR(32)  NOT NULL,
			subject_id         UUID         NOT NULL,
			secret             TEXT         NOT NULL DEFAULT '',
			confirmed_at       TIMESTAMPTZ,
			last_used_counter  BIGINT       NOT NULL DEFAULT 0,
			created_at         TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at         TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT mfa_credentials_pkey PRIMARY KEY (id),
			CONSTRAINT mfa_credentials_subject_type_check CHECK (subject_type IN ('users', 'platform_admins')),
			CONSTRAINT mfa_credentials_subject_unique UNIQUE (subject_type, subject_id)
		)`)
	return err
}

func (r *M00000000000500CreateMfaCredentialsTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS mfa_credentials`)
	return err
}

// M00000000000510CreateMfaBackupCodesTable moves totp_recovery_codes and
// users.totp_secret into the shared MFA tables. The old secret column is
// cleared only after the copy matches. A failed copy leaves the column in
// place. The column and totp_recovery_codes stay, so a row that was not
// copied can still be read.
type M00000000000510CreateMfaBackupCodesTable struct{}

func (r *M00000000000510CreateMfaBackupCodesTable) Signature() string {
	return "00000000000510_create_mfa_backup_codes_table"
}

func (r *M00000000000510CreateMfaBackupCodesTable) Up() error {
	if _, err := facades.Orm().Query().Exec(`
		CREATE TABLE IF NOT EXISTS mfa_backup_codes (
			id           UUID        NOT NULL,
			subject_type VARCHAR(32) NOT NULL,
			subject_id   UUID        NOT NULL,
			code_hash    TEXT        NOT NULL,
			used_at      TIMESTAMPTZ,
			created_at   TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at   TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT mfa_backup_codes_pkey PRIMARY KEY (id),
			CONSTRAINT mfa_backup_codes_subject_type_check CHECK (subject_type IN ('users', 'platform_admins'))
		)`); err != nil {
		return err
	}
	if _, err := facades.Orm().Query().Exec(`
		CREATE INDEX IF NOT EXISTS mfa_backup_codes_subject_index
		ON mfa_backup_codes (subject_type, subject_id)`); err != nil {
		return err
	}
	return facades.Orm().Transaction(copyLegacyMFA)
}

func (r *M00000000000510CreateMfaBackupCodesTable) Down() error {
	if err := facades.Orm().Transaction(restoreLegacyMFA); err != nil {
		return err
	}
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS mfa_backup_codes`)
	return err
}

type legacyTotpRow struct {
	ID      uuid.UUID
	Secret  string
	Enabled bool
	Counter int64
}

type legacyRecoveryRow struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	CodeHash string
	UsedAt   *time.Time
}

func copyLegacyMFA(tx orm.Query) error {
	var users []legacyTotpRow
	if err := tx.Raw(`
		SELECT id,
		       COALESCE(totp_secret, '') AS secret,
		       totp_enabled AS enabled,
		       totp_last_used_counter AS counter
		FROM users
		WHERE COALESCE(totp_secret, '') <> '' OR totp_enabled OR totp_last_used_counter <> 0
		FOR UPDATE`).Scan(&users); err != nil {
		return fmt.Errorf("load totp rows: %w", err)
	}
	for _, row := range users {
		if err := insertCredentialIfMissing(tx, row); err != nil {
			return err
		}
	}
	if err := requireSecretsCopied(tx, users); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE users AS u
		SET totp_secret = NULL
		WHERE COALESCE(u.totp_secret, '') <> ''
		  AND EXISTS (
		    SELECT 1 FROM mfa_credentials AS m
		    WHERE m.subject_type = ?
		      AND m.subject_id = u.id
		      AND m.secret = CASE
		        WHEN u.totp_secret LIKE 'enc:v1:%' THEN u.totp_secret
		        ELSE 'enc:v1:' || u.totp_secret
		      END
		  )`, models.MFASubjectUsers); err != nil {
		return fmt.Errorf("clear copied totp secrets: %w", err)
	}
	left, err := countQuery(tx, `SELECT count(*) FROM users WHERE COALESCE(totp_secret, '') <> ''`)
	if err != nil {
		return err
	}
	if left > 0 {
		return fmt.Errorf("mfa credentials: %d totp secrets were left on users", left)
	}

	var codes []legacyRecoveryRow
	if err := tx.Raw(`
		SELECT id, user_id, code_hash, used_at
		FROM totp_recovery_codes
		FOR UPDATE`).Scan(&codes); err != nil {
		return fmt.Errorf("load recovery codes: %w", err)
	}
	for _, code := range codes {
		if err := insertBackupCodeIfMissing(tx, code); err != nil {
			return err
		}
	}
	uncopied, err := countQuery(tx, `
		SELECT count(*) FROM totp_recovery_codes AS c
		WHERE NOT EXISTS (
		  SELECT 1 FROM mfa_backup_codes AS b
		  WHERE b.id = c.id
		    AND b.subject_type = ?
		    AND b.subject_id = c.user_id
		    AND b.code_hash = c.code_hash
		)`, models.MFASubjectUsers)
	if err != nil {
		return err
	}
	if uncopied > 0 {
		return fmt.Errorf("mfa backup codes: %d recovery codes were not copied", uncopied)
	}
	if _, err := tx.Exec(`
		DELETE FROM totp_recovery_codes AS c
		WHERE EXISTS (
		  SELECT 1 FROM mfa_backup_codes AS b
		  WHERE b.id = c.id
		    AND b.subject_type = ?
		    AND b.subject_id = c.user_id
		    AND b.code_hash = c.code_hash
		)`, models.MFASubjectUsers); err != nil {
		return fmt.Errorf("remove copied recovery codes: %w", err)
	}
	left, err = countQuery(tx, `SELECT count(*) FROM totp_recovery_codes`)
	if err != nil {
		return err
	}
	if left > 0 {
		return fmt.Errorf("mfa backup codes: %d recovery codes were left behind", left)
	}
	return nil
}

func insertCredentialIfMissing(tx orm.Query, row legacyTotpRow) error {
	present, err := countQuery(tx, `
		SELECT count(*) FROM mfa_credentials
		WHERE subject_type = ? AND subject_id = ?`, models.MFASubjectUsers, row.ID)
	if err != nil {
		return err
	}
	if present > 0 {
		return nil
	}
	var confirmed any
	if row.Enabled {
		confirmed = time.Now().UTC()
	}
	if _, err := tx.Exec(`
		INSERT INTO mfa_credentials (
			id, subject_type, subject_id, secret, confirmed_at, last_used_counter, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		uuid.New(), models.MFASubjectUsers, row.ID, settings.PrefixSeal(row.Secret), confirmed, row.Counter,
	); err != nil {
		return fmt.Errorf("insert mfa credential: %w", err)
	}
	return nil
}

func requireSecretsCopied(tx orm.Query, rows []legacyTotpRow) error {
	failed := 0
	for _, row := range rows {
		if row.Secret == "" {
			continue
		}
		var stored string
		if err := tx.Raw(`
			SELECT secret FROM mfa_credentials
			WHERE subject_type = ? AND subject_id = ?`, models.MFASubjectUsers, row.ID).Scan(&stored); err != nil {
			return fmt.Errorf("read copied totp secret: %w", err)
		}
		if stored != settings.PrefixSeal(row.Secret) {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("mfa credentials: %d totp secrets were not copied", failed)
	}
	return nil
}

func insertBackupCodeIfMissing(tx orm.Query, code legacyRecoveryRow) error {
	if code.ID == uuid.Nil || code.UserID == uuid.Nil || code.CodeHash == "" {
		return fmt.Errorf("copy recovery code: id, user and hash are required")
	}
	present, err := countQuery(tx, `SELECT count(*) FROM mfa_backup_codes WHERE id = ?`, code.ID)
	if err != nil {
		return err
	}
	if present > 0 {
		return nil
	}
	if _, err := tx.Exec(`
		INSERT INTO mfa_backup_codes (
			id, subject_type, subject_id, code_hash, used_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		code.ID, models.MFASubjectUsers, code.UserID, code.CodeHash, code.UsedAt,
	); err != nil {
		return fmt.Errorf("insert mfa backup code: %w", err)
	}
	return nil
}

func restoreLegacyMFA(tx orm.Query) error {
	if _, err := tx.Exec(`
		UPDATE users AS u
		SET totp_secret = CASE
		      WHEN m.secret LIKE 'enc:v1:%' THEN substr(m.secret, 8)
		      ELSE NULLIF(m.secret, '')
		    END,
		    totp_last_used_counter = m.last_used_counter,
		    totp_enabled = (m.confirmed_at IS NOT NULL)
		FROM mfa_credentials AS m
		WHERE m.subject_type = ?
		  AND m.subject_id = u.id
		  AND COALESCE(u.totp_secret, '') = ''
		  AND m.secret <> ''`, models.MFASubjectUsers); err != nil {
		return fmt.Errorf("restore totp secrets: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO totp_recovery_codes (id, user_id, code_hash, used_at, created_at, updated_at)
		SELECT b.id, b.subject_id, b.code_hash, b.used_at, NOW(), NOW()
		FROM mfa_backup_codes AS b
		WHERE b.subject_type = ?
		  AND NOT EXISTS (SELECT 1 FROM totp_recovery_codes AS c WHERE c.id = b.id)`,
		models.MFASubjectUsers); err != nil {
		return fmt.Errorf("restore recovery codes: %w", err)
	}
	return nil
}

func countQuery(tx orm.Query, query string, args ...any) (int64, error) {
	var n int64
	if err := tx.Raw(query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	if n < 0 {
		return 0, fmt.Errorf("count is negative")
	}
	return n, nil
}
