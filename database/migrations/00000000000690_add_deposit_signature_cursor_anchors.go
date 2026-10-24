package migrations

import "github.com/goravel/framework/contracts/database/schema"

// M00000000000690AddDepositSignatureCursorAnchors stores, beside each end a backfill
// lists below, the signature recorded right above it: backfill_anchor above
// backfill_before, ahead_anchor above ahead_before. A listing below an end reads from
// right below its anchor and must find the end as its first entry, so only an
// address index that holds the end where it was recorded is read (the index-side
// proof the deposit signature sync asks of a provider). Empty is no anchor known: the
// end is the only recorded signature of its stretch, or the row predates this
// migration (the sync then reads that stretch again from its newest signature).
type M00000000000690AddDepositSignatureCursorAnchors struct{}

func (r *M00000000000690AddDepositSignatureCursorAnchors) Signature() string {
	return "00000000000690_add_deposit_signature_cursor_anchors"
}

func (r *M00000000000690AddDepositSignatureCursorAnchors) Up() error {
	for _, column := range []string{"backfill_anchor", "ahead_anchor"} {
		if migrationSchema().HasColumn("deposit_signature_cursors", column) {
			continue
		}
		if err := migrationSchema().Table("deposit_signature_cursors", func(table schema.Blueprint) {
			table.String(column, 128).Default("")
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000690AddDepositSignatureCursorAnchors) Down() error {
	for _, column := range []string{"ahead_anchor", "backfill_anchor"} {
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
