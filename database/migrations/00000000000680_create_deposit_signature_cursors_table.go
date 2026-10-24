package migrations

import "github.com/goravel/framework/contracts/database/schema"

// M00000000000680CreateDepositSignatureCursorsTable stores how far the deposit
// signature sync of each account of a chain got (Solana: an owner address or one of
// its token accounts, read with getSignaturesForAddress). The recorded signatures are
// one stretch of the account's history: from the newest one (signature, at slot; an
// empty signature: none yet, slot is then where the account is known empty up to)
// down to the oldest one (backfill_before), which the backfill continues below until
// it reaches synced_from_slot, the floor fixed when the account was first read
// (covered). One row per (chain, account).
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
		table.String("backfill_before", 128).Default("")
		table.BigInteger("synced_from_slot").Default(0)
		table.Boolean("covered").Default(false)
		table.TimestampsTz()
		table.Primary("chain", "account")
	})
}

func (r *M00000000000680CreateDepositSignatureCursorsTable) Down() error {
	return migrationSchema().DropIfExists("deposit_signature_cursors")
}
