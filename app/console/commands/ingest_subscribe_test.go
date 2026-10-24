package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	mocksconsole "github.com/goravel/framework/mocks/console"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/ingestsubscription"
)

type fakeSubscriber struct {
	requests []ingestsubscription.Request
	plan     ingestsubscription.Plan
	err      error
	listed   []ingestsubscription.Listed
}

func (f *fakeSubscriber) Subscribe(_ context.Context, req ingestsubscription.Request) (ingestsubscription.Plan, error) {
	f.requests = append(f.requests, req)
	return f.plan, f.err
}

func (f *fakeSubscriber) List(context.Context) ([]ingestsubscription.Listed, error) {
	return f.listed, f.err
}

var subscriptionID = uuid.MustParse("3b9c2f10-4d2e-4a8b-9c61-0f2d4e6a8b1c")

var tethPlan = ingestsubscription.Plan{
	Provider: "alchemy", ChainID: "teth", Network: "ethereum-sepolia", ProviderNetwork: "ETH_SEPOLIA",
	WebhookURL: "https://wallets.example.com/v1/webhooks/ingest/alchemy/teth", Addresses: 2,
	BaseURLSource: "the ingest_push.base_url platform setting",
}

func subscribeContext(t *testing.T, apply bool) *mocksconsole.Context {
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("provider").Return("alchemy")
	ctx.EXPECT().ArgumentString("chain").Return("teth")
	ctx.EXPECT().OptionBool("apply").Return(apply)
	return ctx
}

func TestIngest_Subscribe_SignatureArgumentsAndFlags(t *testing.T) {
	cmd := NewIngestSubscribe(&fakeSubscriber{})
	ext := cmd.Extend()
	assert.Equal(t, "ingest:subscribe", cmd.Signature())
	assert.Equal(t, "ingest", ext.Category)
	assert.Len(t, ext.Arguments, 2)
	assert.Len(t, ext.Flags, 1)
}

func TestIngest_Subscribe_ConstructorRequiresTheService(t *testing.T) {
	assert.Panics(t, func() { NewIngestSubscribe(nil) })
	assert.Panics(t, func() { NewIngestSubscriptions(nil) })
}

func TestIngest_Subscribe_DryRunPrintsThePlan(t *testing.T) {
	subscriber := &fakeSubscriber{plan: tethPlan}
	ctx := subscribeContext(t, false)
	ctx.EXPECT().Info("alchemy subscription of teth: network ethereum-sepolia (ETH_SEPOLIA), webhook https://wallets.example.com/v1/webhooks/ingest/alchemy/teth, 2 watched addresses").Return()
	ctx.EXPECT().Info("base URL read from the ingest_push.base_url platform setting").Return()
	ctx.EXPECT().Info("dry run: nothing created (pass --apply)").Return()

	require.NoError(t, NewIngestSubscribe(subscriber).Handle(ctx))
	assert.Equal(t, []ingestsubscription.Request{{Provider: "alchemy", ChainID: "teth"}}, subscriber.requests)
}

func TestIngest_Subscribe_DryRunSaysTheBaseURLCameFromTheEnvironment(t *testing.T) {
	plan := tethPlan
	plan.BaseURLSource = "WEBHOOK_INGEST_BASE_URL (vault.webhooks.ingest_base_url)"
	ctx := subscribeContext(t, false)
	ctx.EXPECT().Info(mock.MatchedBy(func(line string) bool { return strings.HasPrefix(line, "alchemy subscription of teth:") })).Return()
	ctx.EXPECT().Info("base URL read from WEBHOOK_INGEST_BASE_URL (vault.webhooks.ingest_base_url)").Return()
	ctx.EXPECT().Info("dry run: nothing created (pass --apply)").Return()

	require.NoError(t, NewIngestSubscribe(&fakeSubscriber{plan: plan}).Handle(ctx))
}

func TestIngest_Subscribe_ApplyReportsTheSubscription(t *testing.T) {
	plan := tethPlan
	plan.Applied, plan.SubscriptionID, plan.ProviderWebhookID = true, subscriptionID, "wh_123"
	subscriber := &fakeSubscriber{plan: plan}
	ctx := subscribeContext(t, true)
	ctx.EXPECT().Info(mock.Anything).Return().Twice()
	ctx.EXPECT().Info("created subscription 3b9c2f10-4d2e-4a8b-9c61-0f2d4e6a8b1c, provider webhook wh_123, signing secret stored sealed").Return()

	require.NoError(t, NewIngestSubscribe(subscriber).Handle(ctx))
	assert.True(t, subscriber.requests[0].Apply)
}

func TestIngest_Subscribe_ExplainsWhatIsMissingAndFails(t *testing.T) {
	subscriber := &fakeSubscriber{
		plan: ingestsubscription.Plan{Provider: "alchemy", ChainID: "teth", Missing: []string{"WEBHOOK_INGEST_BASE_URL (...)", "ALCHEMY_AUTH_TOKEN (...)"}},
		err:  ingestsubscription.ErrMissingConfiguration,
	}
	ctx := subscribeContext(t, true)
	ctx.EXPECT().Warning("missing: WEBHOOK_INGEST_BASE_URL (...)").Return()
	ctx.EXPECT().Warning("missing: ALCHEMY_AUTH_TOKEN (...)").Return()
	ctx.EXPECT().Error(mock.Anything).Return()

	err := NewIngestSubscribe(subscriber).Handle(ctx)
	assert.ErrorIs(t, err, ingestsubscription.ErrMissingConfiguration)
}

func TestIngest_Subscribe_AnExistingSubscriptionIsNothingToDo(t *testing.T) {
	plan := tethPlan
	plan.Existing = &ingestsubscription.Listed{ID: subscriptionID, Provider: "alchemy", ChainID: "teth", ProviderWebhookID: "wh_123", Host: "wallets.example.com", Status: "active", SyncStatus: "synced"}
	ctx := subscribeContext(t, true)
	ctx.EXPECT().Info("the alchemy subscription of teth exists: 3b9c2f10-4d2e-4a8b-9c61-0f2d4e6a8b1c, status active, sync synced (last never), host wallets.example.com, provider webhook wh_123; nothing to do").Return()

	require.NoError(t, NewIngestSubscribe(&fakeSubscriber{plan: plan}).Handle(ctx))
}

func TestIngest_Subscribe_AFailedSyncAfterTheRowIsStoredExitsNonZero(t *testing.T) {
	plan := tethPlan
	plan.Applied, plan.SubscriptionID, plan.ProviderWebhookID = true, subscriptionID, "wh_123"
	ctx := subscribeContext(t, true)
	ctx.EXPECT().Info(mock.Anything).Return().Times(3)
	ctx.EXPECT().Error(mock.Anything).Return()

	err := NewIngestSubscribe(&fakeSubscriber{plan: plan, err: ingestsubscription.ErrAddressSyncFailed}).Handle(ctx)
	assert.ErrorIs(t, err, ingestsubscription.ErrAddressSyncFailed)
}

func TestIngest_Subscriptions_ListsEachSubscriptionByHost(t *testing.T) {
	synced := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	lister := &fakeSubscriber{listed: []ingestsubscription.Listed{{
		ID: subscriptionID, Provider: "alchemy", ChainID: "teth", ProviderWebhookID: "wh_123",
		Host: "wallets.example.com", Status: "active", SyncStatus: "synced", LastSyncedAt: &synced,
	}}}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().Line("alchemy teth: 3b9c2f10-4d2e-4a8b-9c61-0f2d4e6a8b1c, status active, sync synced (last 2026-10-20T12:00:00Z), host wallets.example.com, provider webhook wh_123").Return()

	cmd := NewIngestSubscriptions(lister)
	require.NoError(t, cmd.Handle(ctx))
	assert.Equal(t, "ingest:subscriptions", cmd.Signature())
}

func TestIngest_Subscriptions_SaysWhenThereIsNone(t *testing.T) {
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().Info("no ingest subscriptions (create one with ingest:subscribe)").Return()

	require.NoError(t, NewIngestSubscriptions(&fakeSubscriber{}).Handle(ctx))
}

func TestIngest_Subscriptions_FailsWhenTheListCannotBeRead(t *testing.T) {
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().Error(mock.Anything).Return()

	err := NewIngestSubscriptions(&fakeSubscriber{err: errors.New("connection refused")}).Handle(ctx)
	assert.Error(t, err)
}

func TestIngest_Subscribe_DryRunWarnsThatTheSettingWasNotRead(t *testing.T) {
	plan := tethPlan
	plan.BaseURLSource = "WEBHOOK_INGEST_BASE_URL (vault.webhooks.ingest_base_url)"
	plan.BaseURLNote = "the ingest_push.base_url platform setting could not be read: this dry run uses WEBHOOK_INGEST_BASE_URL, and --apply refuses to run until the setting can be read"
	ctx := subscribeContext(t, false)
	ctx.EXPECT().Warning(plan.BaseURLNote).Return()
	ctx.EXPECT().Info(mock.MatchedBy(func(line string) bool { return strings.HasPrefix(line, "alchemy subscription of teth:") })).Return()
	ctx.EXPECT().Info("base URL read from WEBHOOK_INGEST_BASE_URL (vault.webhooks.ingest_base_url)").Return()
	ctx.EXPECT().Info("dry run: nothing created (pass --apply)").Return()

	require.NoError(t, NewIngestSubscribe(&fakeSubscriber{plan: plan}).Handle(ctx))
}

func TestIngest_Subscribe_ApplyFailsWhenTheSettingCannotBeRead(t *testing.T) {
	subscriber := &fakeSubscriber{
		plan: ingestsubscription.Plan{Provider: "alchemy", ChainID: "teth", Network: "ethereum-sepolia", ProviderNetwork: "ETH_SEPOLIA"},
		err:  fmt.Errorf("%w: connection refused", ingestsubscription.ErrBaseURLUnread),
	}
	ctx := subscribeContext(t, true)
	ctx.EXPECT().Error(mock.MatchedBy(func(line string) bool { return strings.Contains(line, "nothing was created") })).Return()

	err := NewIngestSubscribe(subscriber).Handle(ctx)

	assert.ErrorIs(t, err, ingestsubscription.ErrBaseURLUnread)
}

func TestIngest_Subscribe_DryRunWarnsThatTheBaseURLResolvesToARefusedAddress(t *testing.T) {
	plan := tethPlan
	plan.BaseURLWarning = "the url host resolves to an address that is not allowed (base URL read from the ingest_push.base_url platform setting): --apply refuses it"
	ctx := subscribeContext(t, false)
	ctx.EXPECT().Warning(plan.BaseURLWarning).Return()
	ctx.EXPECT().Info(mock.MatchedBy(func(line string) bool { return strings.HasPrefix(line, "alchemy subscription of teth:") })).Return()
	ctx.EXPECT().Info("base URL read from the ingest_push.base_url platform setting").Return()
	ctx.EXPECT().Info("dry run: nothing created (pass --apply)").Return()

	require.NoError(t, NewIngestSubscribe(&fakeSubscriber{plan: plan}).Handle(ctx))
}

func TestIngest_Subscribe_ApplyFailsOnAStoredSettingTheRuleRefuses(t *testing.T) {
	subscriber := &fakeSubscriber{
		plan: ingestsubscription.Plan{Provider: "alchemy", ChainID: "teth", Network: "ethereum-sepolia", ProviderNetwork: "ETH_SEPOLIA"},
		err:  ingestsubscription.ErrBaseURLSettingInvalid,
	}
	ctx := subscribeContext(t, true)
	ctx.EXPECT().Error(mock.MatchedBy(func(line string) bool {
		return strings.Contains(line, "ingest_push.base_url") && strings.Contains(line, "nothing was created")
	})).Return()

	err := NewIngestSubscribe(subscriber).Handle(ctx)

	assert.ErrorIs(t, err, ingestsubscription.ErrBaseURLSettingInvalid)
}
