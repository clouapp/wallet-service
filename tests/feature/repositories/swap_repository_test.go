package repositories_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/pkg/pgerr"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/stretchr/testify/require"
)

func TestSwapRepository_ClaimsOnceScopesReadsAndFreezesFunding(t *testing.T) {
	fixtures.TestDB(t)
	require.NoError(t, (&migrations.M00000000000720CreateSwaps{}).Up(), "migration can be repeated")
	account := fixtures.InsertAccount(t, "swap-owner")
	source := fixtures.InsertWalletWithAccount(t, "eth", &account.ID)
	destination := fixtures.InsertWalletWithAccount(t, "btc", &account.ID)
	repo := repositories.NewSwapRepository(nil)
	ctx := context.Background()
	quote := &models.SwapQuote{ID: uuid.New(), AccountID: account.ID, SourceWalletID: source.ID, DestinationWalletID: destination.ID, Provider: "lifi", RouteID: "pinned", RequestPayload: "{}", RoutePayload: "{}", FeesPayload: "[]", AttemptsPayload: "[]", ExpectedRaw: "1000", MinimumRaw: "990", ExpiresAt: time.Now().Add(time.Minute)}
	require.NoError(t, repo.CreateQuote(ctx, quote))
	invalid := *quote
	invalid.ID, invalid.MinimumRaw = uuid.New(), "1001"
	require.Error(t, repo.CreateQuote(ctx, &invalid), "the migrated database rejects a minimum greater than the expected output")
	_, err := repo.FindQuote(ctx, uuid.New(), source.ID, quote.ID)
	require.ErrorIs(t, err, models.ErrRepositoryNotFound)
	_, err = repo.FindQuote(ctx, account.ID, destination.ID, quote.ID)
	require.ErrorIs(t, err, models.ErrRepositoryNotFound)
	order := &models.SwapOrder{ID: uuid.New(), AccountID: account.ID, QuoteID: quote.ID, SourceWalletID: source.ID, DestinationWalletID: destination.ID, IdempotencyKey: uuid.New(), Provider: "lifi", Status: "preparing", FundingPayload: "{}", ReceivedRaw: "0", RefundRaw: "0", AuthorizedUntil: time.Now().Add(time.Minute)}
	require.NoError(t, repo.CreateOrder(ctx, order))
	duplicate := *order
	duplicate.ID = uuid.New()
	duplicate.IdempotencyKey = uuid.New()
	require.True(t, pgerr.IsUniqueViolation(repo.CreateOrder(ctx, &duplicate)), "a second key cannot spend the same quote")
	frozen := `{"kind":"evm","to":"0xaccepted"}`
	require.NoError(t, repo.SaveFunding(ctx, order.ID, frozen))
	require.NoError(t, repo.SaveFunding(ctx, order.ID, frozen))
	require.Error(t, repo.SaveFunding(ctx, order.ID, `{"to":"0xchanged"}`), "funding cannot be replaced on a continuation")
	transactionID := uuid.New()
	tx := gasPreparationRow(source.ID, transactionID)
	step := &models.SwapExecutionStep{ID: uuid.New(), OrderID: order.ID, TransactionID: transactionID, Position: 0, Purpose: "funding", TxHash: tx.TxHash}
	require.NoError(t, repo.RecordSigned(ctx, step, tx))
	saved, err := repo.FindOrder(ctx, account.ID, source.ID, order.ID)
	require.NoError(t, err)
	require.Equal(t, tx.TxHash, saved.SourceTxHash)
	require.Equal(t, "reconciling", saved.Status)
	steps, err := repo.ExecutionSteps(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, steps, 1)
	require.Error(t, repo.RecordSource(ctx, order.ID, "different-hash", "broadcast"), "a recorded source hash is immutable")
	transactionID2 := uuid.New()
	tx2 := gasPreparationRow(source.ID, transactionID2)
	step2 := *step
	step2.ID = uuid.New()
	step2.TransactionID = transactionID2
	require.Error(t, repo.RecordSigned(ctx, &step2, tx2))
	_, err = repositories.NewTransactionRepository(nil).FindByID(ctx, transactionID2)
	require.ErrorIs(t, err, models.ErrRepositoryNotFound, "step collision rolls back its transaction row")
	// Two provider orders can share one batched payout transaction. Concurrent
	// allocation must not turn a confirmed total of 100 into two credits of 60:
	// the second report is capped to the 40 the payout still holds.
	quote2 := *quote
	quote2.ID = uuid.New()
	require.NoError(t, repo.CreateQuote(ctx, &quote2))
	order2 := *order
	order2.ID = uuid.New()
	order2.QuoteID = quote2.ID
	order2.IdempotencyKey = uuid.New()
	require.NoError(t, repo.CreateOrder(ctx, &order2))
	credits := []*models.SwapCreditAllocation{}
	for _, id := range []uuid.UUID{order.ID, order2.ID} {
		credits = append(credits, &models.SwapCreditAllocation{ID: uuid.New(), OrderID: id, WalletID: destination.ID, Direction: "receive", Chain: "btc", AssetKey: "native", Address: "batched-recipient", TxHash: strings.Repeat("a", 64), AmountRaw: "60"})
	}
	type allocationResult struct {
		credit    *models.SwapCreditAllocation
		raw       string
		confirmed bool
		err       error
	}
	results := make(chan allocationResult, 2)
	for _, credit := range credits {
		go func(credit *models.SwapCreditAllocation) {
			raw, confirmed, err := repo.ClaimCredit(ctx, credit, func(context.Context) (string, error) { return "100", nil })
			results <- allocationResult{credit, raw, confirmed, err}
		}(credit)
	}
	claimed := map[string]*models.SwapCreditAllocation{}
	for range 2 {
		result := <-results
		require.NoError(t, result.err)
		require.True(t, result.confirmed)
		claimed[result.raw] = result.credit
	}
	require.Len(t, claimed, 2)
	require.NotNil(t, claimed["60"], "the first allocation keeps its report")
	require.NotNil(t, claimed["40"], "the second is capped to what the payout still holds")
	third := &models.SwapCreditAllocation{ID: uuid.New(), OrderID: uuid.New(), WalletID: destination.ID, Direction: "receive", Chain: "btc", AssetKey: "native", Address: "batched-recipient", TxHash: strings.Repeat("a", 64), AmountRaw: "1"}
	raw, confirmed, err := repo.ClaimCredit(ctx, third, func(context.Context) (string, error) { return "100", nil })
	require.NoError(t, err)
	require.False(t, confirmed, "a fully allocated payout credits nothing more")
	require.Equal(t, "0", raw)
	replay := *claimed["60"]
	replay.ID, replay.AmountRaw = uuid.New(), "100"
	raw, confirmed, err = repo.ClaimCredit(ctx, &replay, func(context.Context) (string, error) {
		t.Fatal("a replay must use its recorded allocation")
		return "0", nil
	})
	require.NoError(t, err)
	require.True(t, confirmed)
	require.Equal(t, "60", raw)
	open, err := repo.ListOpenOrders(ctx, 10)
	require.NoError(t, err)
	require.NotEmpty(t, open, "accepted orders are listed for the reconciler")
	for _, row := range open {
		require.Contains(t, []string{"preparing", "broadcast", "processing", "reconciling", "refund_pending"}, row.Status)
	}
}
