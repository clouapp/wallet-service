package migrations

import "github.com/goravel/framework/contracts/database/schema"

// M00000000000700AddDepositSignatureCursorStates stores, beside each account's cursor,
// the state the signature sync last saw the account in (account_state: what the
// chain's adapter reads of it in one batch for many accounts, Solana its lamports,
// owner program and token amount) and the slot it was read at (account_state_slot).
// The sync reads an account's history again only once its state differs, so a cycle
// finds the accounts that moved among thousands in a few calls. Empty is no state
// known: the row predates this migration, or the account did not exist then.
type M00000000000700AddDepositSignatureCursorStates struct{}

func (r *M00000000000700AddDepositSignatureCursorStates) Signature() string {
	return "00000000000700_add_deposit_signature_cursor_states"
}

func (r *M00000000000700AddDepositSignatureCursorStates) Up() error {
	if !migrationSchema().HasColumn("deposit_signature_cursors", "account_state") {
		if err := migrationSchema().Table("deposit_signature_cursors", func(table schema.Blueprint) {
			table.String("account_state", 255).Default("")
		}); err != nil {
			return err
		}
	}
	if !migrationSchema().HasColumn("deposit_signature_cursors", "account_state_slot") {
		if err := migrationSchema().Table("deposit_signature_cursors", func(table schema.Blueprint) {
			table.BigInteger("account_state_slot").Default(0)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000700AddDepositSignatureCursorStates) Down() error {
	for _, column := range []string{"account_state_slot", "account_state"} {
		if !migrationSchema().HasColumn("deposit_signature_cursors", column) {
			continue
		}
		if err := migrationSchema().Table("deposit_signature_cursors", func(table schema.Blueprint) {
			table.DropColumn(column)
		}); err != nil {
			return err
		}
	}
	return nil
}
