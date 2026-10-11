package migrations

import "github.com/goravel/framework/contracts/database/schema"

// M00000000000610CreateDepositScanCheckpointsTable stores where the deposit scan of
// each chain resumes: the last block whose deposits were all recorded or saved as
// pending. Redis keeps only a cache of it (vault:checkpoint:<chain>), so a lost key
// no longer restarts the chain at the head. A chain without a row was never scanned.
type M00000000000610CreateDepositScanCheckpointsTable struct{}

func (r *M00000000000610CreateDepositScanCheckpointsTable) Signature() string {
	return "00000000000610_create_deposit_scan_checkpoints_table"
}

func (r *M00000000000610CreateDepositScanCheckpointsTable) Up() error {
	if migrationSchema().HasTable("deposit_scan_checkpoints") {
		return nil
	}
	return migrationSchema().Create("deposit_scan_checkpoints", func(table schema.Blueprint) {
		table.String("chain", 50)
		table.BigInteger("block_number")
		table.TimestampsTz()
		table.Primary("chain")
	})
}

func (r *M00000000000610CreateDepositScanCheckpointsTable) Down() error {
	return migrationSchema().DropIfExists("deposit_scan_checkpoints")
}
