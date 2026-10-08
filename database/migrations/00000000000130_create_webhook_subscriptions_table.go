package migrations

import "github.com/goravel/framework/facades"

type M00000000000130CreateWebhookSubscriptionsTable struct{}

func (r *M00000000000130CreateWebhookSubscriptionsTable) Signature() string {
	return "00000000000130_create_webhook_subscriptions_table"
}

func (r *M00000000000130CreateWebhookSubscriptionsTable) Up() error {
	stmts := []string{
		`CREATE TABLE webhook_subscriptions (
			id                     UUID         NOT NULL,
			chain_id               VARCHAR(20)  NOT NULL,
			provider               VARCHAR(20)  NOT NULL,
			provider_webhook_id    VARCHAR(255) NOT NULL,
			webhook_url            TEXT         NOT NULL,
			signing_secret         TEXT         NOT NULL,
			status                 VARCHAR(20)  NOT NULL DEFAULT 'active',
			sync_status            VARCHAR(20)  NOT NULL DEFAULT 'synced',
			synced_addresses_hash  VARCHAR(64),
			last_synced_at         TIMESTAMPTZ,
			created_at             TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at             TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT webhook_subscriptions_pkey              PRIMARY KEY (id),
			CONSTRAINT webhook_subscriptions_chain_id_foreign  FOREIGN KEY (chain_id) REFERENCES chains(id)
		)`,
		`CREATE INDEX idx_ws_chain_provider ON webhook_subscriptions (chain_id, provider)`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000130CreateWebhookSubscriptionsTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS webhook_subscriptions`)
	return err
}
