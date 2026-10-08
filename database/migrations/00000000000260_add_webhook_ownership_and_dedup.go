package migrations

import "github.com/goravel/framework/facades"

type M00000000000260AddWebhookOwnershipAndDedup struct{}

func (r *M00000000000260AddWebhookOwnershipAndDedup) Signature() string {
	return "00000000000260_add_webhook_ownership_and_dedup"
}

func (r *M00000000000260AddWebhookOwnershipAndDedup) Up() error {
	stmts := []string{
		`ALTER TABLE webhook_configs ADD COLUMN IF NOT EXISTS account_id UUID`,
		`CREATE INDEX IF NOT EXISTS webhook_configs_account_id_index ON webhook_configs (account_id)`,
		`COMMENT ON COLUMN webhook_configs.account_id IS 'Owning account; NULL for legacy configs created before ownership was recorded'`,
		`ALTER TABLE webhook_events ADD COLUMN IF NOT EXISTS webhook_config_id UUID`,
		`ALTER TABLE webhook_events ADD COLUMN IF NOT EXISTS subject_id VARCHAR(64)`,
		`CREATE INDEX IF NOT EXISTS webhook_events_pending_delivery_index ON webhook_events (delivery_status, updated_at) WHERE delivery_status = 'pending'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS webhook_events_subject_dedup_unique ON webhook_events (webhook_config_id, event_type, subject_id) WHERE subject_id IS NOT NULL AND webhook_config_id IS NOT NULL`,
		`COMMENT ON COLUMN webhook_events.webhook_config_id IS 'Config the event is delivered for; used to sign retries'`,
		`COMMENT ON COLUMN webhook_events.subject_id IS 'Business id the event describes (withdrawal id); one event per config, type and subject'`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000260AddWebhookOwnershipAndDedup) Down() error {
	stmts := []string{
		`DROP INDEX IF EXISTS webhook_events_subject_dedup_unique`,
		`DROP INDEX IF EXISTS webhook_events_pending_delivery_index`,
		`ALTER TABLE webhook_events DROP COLUMN IF EXISTS subject_id`,
		`ALTER TABLE webhook_events DROP COLUMN IF EXISTS webhook_config_id`,
		`DROP INDEX IF EXISTS webhook_configs_account_id_index`,
		`ALTER TABLE webhook_configs DROP COLUMN IF EXISTS account_id`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}
