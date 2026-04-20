package migrations

import "github.com/goravel/framework/facades"

type M00000000000120CreateWebhookEventsTable struct{}

func (r *M00000000000120CreateWebhookEventsTable) Signature() string {
	return "00000000000120_create_webhook_events_table"
}

func (r *M00000000000120CreateWebhookEventsTable) Up() error {
	stmts := []string{
		`CREATE TABLE webhook_events (
			id              UUID         NOT NULL,
			transaction_id  UUID,
			event_type      VARCHAR(50)  NOT NULL,
			payload         TEXT         NOT NULL,
			delivery_url    VARCHAR(500) NOT NULL,
			delivery_status VARCHAR(20)  NOT NULL DEFAULT 'pending',
			attempts        INTEGER      NOT NULL DEFAULT 0,
			max_attempts    INTEGER      NOT NULL DEFAULT 10,
			last_error      TEXT,
			delivered_at    TIMESTAMPTZ,
			created_at      TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at      TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT webhook_events_pkey                   PRIMARY KEY (id),
			CONSTRAINT webhook_events_transaction_id_foreign FOREIGN KEY (transaction_id) REFERENCES transactions(id)
		)`,
		`CREATE INDEX webhook_events_transaction_id_index  ON webhook_events (transaction_id)`,
		`CREATE INDEX webhook_events_delivery_status_index ON webhook_events (delivery_status)`,
		`CREATE INDEX webhook_events_event_type_index      ON webhook_events (event_type)`,
		`COMMENT ON TABLE  webhook_events                 IS 'Webhook delivery events and retry queue'`,
		`COMMENT ON COLUMN webhook_events.transaction_id  IS 'Foreign key to transactions table'`,
		`COMMENT ON COLUMN webhook_events.event_type      IS 'Event type (deposit.confirmed, withdrawal.completed, etc.)'`,
		`COMMENT ON COLUMN webhook_events.payload         IS 'JSON event payload'`,
		`COMMENT ON COLUMN webhook_events.delivery_url    IS 'Target webhook URL'`,
		`COMMENT ON COLUMN webhook_events.delivery_status IS 'pending, delivered, failed'`,
		`COMMENT ON COLUMN webhook_events.attempts        IS 'Number of delivery attempts'`,
		`COMMENT ON COLUMN webhook_events.max_attempts    IS 'Maximum retry attempts'`,
		`COMMENT ON COLUMN webhook_events.last_error      IS 'Last delivery error message'`,
		`COMMENT ON COLUMN webhook_events.delivered_at    IS 'Timestamp of successful delivery'`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000120CreateWebhookEventsTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS webhook_events`)
	return err
}
