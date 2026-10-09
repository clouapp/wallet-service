package migrations

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"
)

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
	if _, err := migrationQuery().Exec(`
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
	if _, err := migrationQuery().Exec(`
		CREATE INDEX IF NOT EXISTS mfa_backup_codes_subject_index
		ON mfa_backup_codes (subject_type, subject_id)`); err != nil {
		return err
	}
	return migrationTransaction(copyLegacyMFA)
}

func (r *M00000000000510CreateMfaBackupCodesTable) Down() error {
	if err := migrationTransaction(restoreLegacyMFA); err != nil {
		return err
	}
	_, err := migrationQuery().Exec(`DROP TABLE IF EXISTS mfa_backup_codes`)
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
		  )`, mfaSubjectUsers); err != nil {
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
		)`, mfaSubjectUsers)
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
		)`, mfaSubjectUsers); err != nil {
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
		WHERE subject_type = ? AND subject_id = ?`, mfaSubjectUsers, row.ID)
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
		uuid.New(), mfaSubjectUsers, row.ID, prefixSeal(row.Secret), confirmed, row.Counter,
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
			WHERE subject_type = ? AND subject_id = ?`, mfaSubjectUsers, row.ID).Scan(&stored); err != nil {
			return fmt.Errorf("read copied totp secret: %w", err)
		}
		if stored != prefixSeal(row.Secret) {
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
		code.ID, mfaSubjectUsers, code.UserID, code.CodeHash, code.UsedAt,
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
		  AND m.secret <> ''`, mfaSubjectUsers); err != nil {
		return fmt.Errorf("restore totp secrets: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO totp_recovery_codes (id, user_id, code_hash, used_at, created_at, updated_at)
		SELECT b.id, b.subject_id, b.code_hash, b.used_at, NOW(), NOW()
		FROM mfa_backup_codes AS b
		WHERE b.subject_type = ?
		  AND NOT EXISTS (SELECT 1 FROM totp_recovery_codes AS c WHERE c.id = b.id)`,
		mfaSubjectUsers); err != nil {
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
