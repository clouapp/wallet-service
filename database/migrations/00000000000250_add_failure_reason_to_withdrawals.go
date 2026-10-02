package migrations

import "github.com/goravel/framework/facades"

type M00000000000250AddFailureReasonToWithdrawals struct{}

func (r *M00000000000250AddFailureReasonToWithdrawals) Signature() string {
	return "00000000000250_add_failure_reason_to_withdrawals"
}

func (r *M00000000000250AddFailureReasonToWithdrawals) Up() error {
	_, err := facades.Orm().Query().Exec(
		`ALTER TABLE withdrawals ADD COLUMN IF NOT EXISTS failure_reason VARCHAR(64)`,
	)
	return err
}

func (r *M00000000000250AddFailureReasonToWithdrawals) Down() error {
	_, err := facades.Orm().Query().Exec(`ALTER TABLE withdrawals DROP COLUMN IF EXISTS failure_reason`)
	return err
}
