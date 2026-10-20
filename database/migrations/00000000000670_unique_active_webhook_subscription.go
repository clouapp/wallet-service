package migrations

import "fmt"

// uniqueActiveSubscriptionIndex is the one active subscription a provider has on a
// chain: ingest:subscribe checks for one and then creates it, and two runs at once
// would both create one (two provider webhooks pushing every deposit twice).
const uniqueActiveSubscriptionIndex = "webhook_subscriptions_active_chain_provider_unique"

// M00000000000670UniqueActiveWebhookSubscription makes the database refuse a second
// active webhook_subscriptions row for one (chain_id, provider); inactive rows stay
// free. Up refuses to run while duplicates exist, naming how many groups.
type M00000000000670UniqueActiveWebhookSubscription struct{}

func (r *M00000000000670UniqueActiveWebhookSubscription) Signature() string {
	return "00000000000670_unique_active_webhook_subscription"
}

func (r *M00000000000670UniqueActiveWebhookSubscription) Up() error {
	var duplicates []struct{ DuplicateGroups int64 }
	if err := migrationQuery().Raw(`
		SELECT COUNT(*) AS duplicate_groups FROM (
			SELECT 1 FROM webhook_subscriptions
			WHERE status = 'active'
			GROUP BY chain_id, provider
			HAVING COUNT(*) > 1
		) duplicated`).Scan(&duplicates); err != nil {
		return fmt.Errorf("count duplicate active subscriptions: %w", err)
	}
	if len(duplicates) == 1 && duplicates[0].DuplicateGroups > 0 {
		return fmt.Errorf("%d (chain_id, provider) active subscription groups have more than one row; disable the extra ones before adding %s", duplicates[0].DuplicateGroups, uniqueActiveSubscriptionIndex)
	}
	_, err := migrationQuery().Exec(`CREATE UNIQUE INDEX IF NOT EXISTS ` + uniqueActiveSubscriptionIndex +
		` ON webhook_subscriptions (chain_id, provider) WHERE status = 'active'`)
	return err
}

func (r *M00000000000670UniqueActiveWebhookSubscription) Down() error {
	_, err := migrationQuery().Exec(`DROP INDEX IF EXISTS ` + uniqueActiveSubscriptionIndex)
	return err
}
