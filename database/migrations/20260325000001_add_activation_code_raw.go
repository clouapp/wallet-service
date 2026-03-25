package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260325000001AddActivationCodeRaw struct{}

func (r *M20260325000001AddActivationCodeRaw) Signature() string {
	return "20260325000001_add_activation_code_raw"
}

func (r *M20260325000001AddActivationCodeRaw) Up() error {
	_, err := facades.Orm().Query().Exec(
		"ALTER TABLE wallets ADD COLUMN IF NOT EXISTS activation_code CHAR(6)",
	)
	return err
}

func (r *M20260325000001AddActivationCodeRaw) Down() error {
	_, err := facades.Orm().Query().Exec(
		"ALTER TABLE wallets DROP COLUMN IF EXISTS activation_code",
	)
	return err
}
