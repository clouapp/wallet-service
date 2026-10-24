package migrations

import "github.com/goravel/framework/contracts/database/schema"

// M00000000000680CreateDepositSignatureCursorsTable stores where the deposit
// signature sync of each account of a chain resumes (Solana: an owner address or one
// of its token accounts, read with getSignaturesForAddress): the newest signature
// whose deposits were all recorded and its slot (an empty signature: none yet, the
// slot is then the floor the history is read from), and the slot the account's
// history was first read from (0: the whole history). One row per (chain, account).
type M00000000000680CreateDepositSignatureCursorsTable struct{}

func (r *M00000000000680CreateDepositSignatureCursorsTable) Signature() string {
	return "00000000000680_create_deposit_signature_cursors_table"
}

func (r *M00000000000680CreateDepositSignatureCursorsTable) Up() error {
	if migrationSchema().HasTable("deposit_signature_cursors") {
		return nil
	}
	return migrationSchema().Create("deposit_signature_cursors", func(table schema.Blueprint) {
		table.String("chain", 50)
		table.String("account", 64)
		table.String("signature", 128).Default("")
		table.BigInteger("slot").Default(0)
		table.BigInteger("synced_from_slot").Default(0)
		table.TimestampsTz()
		table.Primary("chain", "account")
	})
}

func (r *M00000000000680CreateDepositSignatureCursorsTable) Down() error {
	return migrationSchema().DropIfExists("deposit_signature_cursors")
}
