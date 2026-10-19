package deposit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

// witnessChain is a chain whose expiry is proven by witnesses, as Solana's is.
type witnessChain struct {
	*mocks.MockChain
	evidence chain.ExpiryEvidence
	asked    int
}

func (c *witnessChain) ExpiryEvidence(context.Context, string, uint64) (chain.ExpiryEvidence, error) {
	c.asked++
	return c.evidence, nil
}

// expiringWithdrawal is a broadcast withdrawal of chainID whose last valid block is
// 500; unknown marks its broadcast outcome unknown.
func expiringWithdrawal(t *testing.T, walletID uuid.UUID, chainID string, unknown bool) (models.Transaction, models.Withdrawal) {
	t.Helper()
	tx := fixtures.InsertTransaction(t, walletID, nil, chainID, models.TxTypeWithdrawal, "confirming", "sol", "1000", 0)
	payload := map[string]any{chain.LastValidBlockPayloadKey: 500}
	if unknown {
		payload[chain.BroadcastOutcomePayloadKey] = chain.BroadcastOutcomeUnknown
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	withdrawalID := uuid.New()
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", tx.ID).
		Update(map[string]any{"raw_payload": string(raw), "idempotency_key": withdrawalID.String()}); err != nil {
		t.Fatal(err)
	}
	withdrawal := models.Withdrawal{
		ID: withdrawalID, WalletID: walletID, TransactionID: &tx.ID, Status: models.WithdrawalStatusBroadcast,
		Amount: "1", DestinationAddress: "AJor34TKjdm2pAK6V7nkBmKNpnARHL7CkiwnBVTE9J6b", FeeEstimate: "0",
	}
	if err := facades.Orm().Query().Create(&withdrawal); err != nil {
		t.Fatal(err)
	}
	return tx, withdrawal
}

func witnessFixture(t *testing.T, evidence chain.ExpiryEvidence) (*Service, *witnessChain, uuid.UUID, *testClock) {
	t.Helper()
	fixtures.TestDB(t)
	base := mocks.NewMockChain("tsol")
	base.RequiredConfirmationsVal = 1
	base.GetTransactionBlockVal = 0
	adapter := &witnessChain{MockChain: base, evidence: evidence}
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	w := fixtures.InsertWallet(t, "tsol")
	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(newWithdrawalPublisher())
	clock := &testClock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	svc.now = clock.Now
	return svc, adapter, w.ID, clock
}

func TestRunConfirmationCheck_SolanaExpiresWithTwoFreshWitnesses(t *testing.T) {
	svc, _, walletID, _ := witnessFixture(t, chain.ExpiryEvidence{Past: true, Witnesses: 2})
	tx, withdrawal := expiringWithdrawal(t, walletID, "tsol", true)

	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reloadTransaction(t, tx.ID); got.Status != string(types.TxStatusFailed) || got.ErrorMessage != models.WithdrawalFailureExpired {
		t.Fatalf("status %s error %q", got.Status, got.ErrorMessage)
	}
	if got := reloadWithdrawal(t, withdrawal.ID); got.Status != models.WithdrawalStatusFailed {
		t.Fatalf("withdrawal %s", got.Status)
	}
}

func TestRunConfirmationCheck_SolanaOneWitnessMustBeSeenTwiceMinutesApart(t *testing.T) {
	svc, _, walletID, clock := witnessFixture(t, chain.ExpiryEvidence{Past: true, Witnesses: 1})
	tx, _ := expiringWithdrawal(t, walletID, "tsol", true)

	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := reloadTransaction(t, tx.ID)
	if got.Status != string(types.TxStatusConfirming) || !json.Valid([]byte(got.RawPayload)) {
		t.Fatalf("one witness is no proof: status %s", got.Status)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(got.RawPayload), &payload)
	if payload[chain.ExpiryWitnessedAtPayloadKey] != float64(clock.Now().Unix()) {
		t.Fatalf("raw_payload %s: the first sight is kept", got.RawPayload)
	}

	clock.advance(expiryWitnessGap / 2)
	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reloadTransaction(t, tx.ID); got.Status != string(types.TxStatusConfirming) {
		t.Fatalf("a second look too soon proves nothing: %s", got.Status)
	}
	clock.advance(expiryWitnessGap)
	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reloadTransaction(t, tx.ID); got.Status != string(types.TxStatusFailed) {
		t.Fatalf("seen again minutes later: status %s, want failed", got.Status)
	}
}

func TestRunConfirmationCheck_SolanaExpiryIsReadOnlyForUnknownOutcomes(t *testing.T) {
	svc, adapter, walletID, _ := witnessFixture(t, chain.ExpiryEvidence{Past: true, Witnesses: 2})
	accepted, _ := expiringWithdrawal(t, walletID, "tsol", false)

	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reloadTransaction(t, accepted.ID); got.Status != string(types.TxStatusConfirming) || adapter.asked != 0 {
		t.Fatalf("status %s, asked %d: a broadcast the node accepted is never expired automatically", got.Status, adapter.asked)
	}
}
