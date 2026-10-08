package migrations

import "github.com/goravel/framework/facades"

type M00000000000110CreateWebhookConfigsTable struct{}

func (r *M00000000000110CreateWebhookConfigsTable) Signature() string {
	return "00000000000110_create_webhook_configs_table"
}

func (r *M00000000000110CreateWebhookConfigsTable) Up() error {
	stmts := []string{
		`CREATE TABLE webhook_configs (
			id         UUID         NOT NULL,
			url        VARCHAR(500) NOT NULL,
			secret     VARCHAR(255) NOT NULL,
			events     TEXT         NOT NULL,
			is_active  BOOLEAN      NOT NULL DEFAULT TRUE,
			wallet_id  UUID,
			type       VARCHAR(50),
			created_at TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT webhook_configs_pkey PRIMARY KEY (id)
		)`,
		`CREATE INDEX webhook_configs_is_active_index ON webhook_configs (is_active)`,
		`CREATE INDEX webhook_configs_wallet_id_index ON webhook_configs (wallet_id)`,
		`COMMENT ON TABLE  webhook_configs             IS 'Webhook configurations for event notifications'`,
		`COMMENT ON COLUMN webhook_configs.url         IS 'Webhook endpoint URL'`,
		`COMMENT ON COLUMN webhook_configs.secret      IS 'HMAC secret for webhook signature'`,
		`COMMENT ON COLUMN webhook_configs.events      IS 'Comma-separated event types (deposit.confirmed, withdrawal.completed, etc.)'`,
		`COMMENT ON COLUMN webhook_configs.is_active   IS 'Whether webhook is enabled'`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000110CreateWebhookConfigsTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS webhook_configs`)
	return err
}
