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
	// allocation must not turn a confirmed total of 100 into two credits of 60.
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
		confirmed bool
		err       error
	}
	results := make(chan allocationResult, 2)
	for _, credit := range credits {
		go func(credit *models.SwapCreditAllocation) {
			_, confirmed, err := repo.ClaimCredit(ctx, credit, func(context.Context) (string, error) { return "100", nil })
			results <- allocationResult{credit, confirmed, err}
		}(credit)
	}
	var winner, remaining *models.SwapCreditAllocation
	for range 2 {
		result := <-results
		require.NoError(t, result.err)
		if result.confirmed {
			require.Nil(t, winner)
			winner = result.credit
		} else {
			remaining = result.credit
		}
	}
	require.NotNil(t, winner)
	require.NotNil(t, remaining)
	remaining.AmountRaw = "40"
	raw, confirmed, err := repo.ClaimCredit(ctx, remaining, func(context.Context) (string, error) { return "100", nil })
	require.NoError(t, err)
	require.True(t, confirmed)
	require.Equal(t, "40", raw)
	winner.AmountRaw = "100"
	raw, confirmed, err = repo.ClaimCredit(ctx, winner, func(context.Context) (string, error) {
		t.Fatal("a replay must use its recorded allocation")
		return "0", nil
	})
	require.NoError(t, err)
	require.True(t, confirmed)
	require.Equal(t, "60", raw)
}
