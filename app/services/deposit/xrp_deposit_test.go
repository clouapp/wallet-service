package deposit

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/depositevents"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

// Native Payments recorded on the altnet, read by the adapter tests
// (TestLive_ScanBlock_*, app/adapters/chain/xrp/testdata/xrpl): one carrying
// destination tag 777001 in ledger 21451514, one without a tag in ledger 21451387.
const (
	xrpTaggedLedger    = 21451514
	xrpTaggedHash      = "429100F561631FEA1B5EC4BB26B0607E753F0A5157B38824206CD298318C7516"
	xrpTaggedSender    = "rDWd81gTyMMA1arNv9bH1qcBDN4EaozN1Y"
	xrpTaggedRecipient = "rai1iQmERbDPvfuk31XVsYs6ZXeA8QRZnC"
	xrpTaggedDrops     = 1_000_000
	xrpTaggedTag       = uint32(777001)

	xrpUntaggedHash      = "15DFF33BCB07FEF157836ADEFC15D95E05A4B40D67AE6E827D4F547EFA9A4FA8"
	xrpUntaggedSender    = "rJJWTfxZUg6FkYfcNHqrEgG9CUAFvnw7DB"
	xrpUntaggedRecipient = "rwpNHW66K4YcwAGyx7F1ehUW12Yujwy7Ni"
	xrpUntaggedDrops     = 1_468_948

	xrpDepositHookSecret = "xrp-deposit-events-secret"
	xrpDropsDecimals     = 6
)

// xrpLedgerChain is the txrp port for deposit tests: it reports transfers for one
// validated ledger, which is also the tip. Reading recorded ledgers stays in the
// adapter tests.
func xrpLedgerChain(ledger uint64, transfers ...types.DetectedTransfer) *mocks.MockChain {
	adapter := mocks.NewMockChain(models.ChainTXRP)
	adapter.NativeAssetVal = models.NativeXRP
	adapter.RequiredConfirmationsVal = 1
	adapter.GetLatestBlockFn = func(context.Context) (uint64, error) { return ledger, nil }
	adapter.ScanBlockFn = func(_ context.Context, index uint64) ([]types.DetectedTransfer, error) {
		if index != ledger {
			return nil, nil
		}
		return transfers, nil
	}
	return adapter
}

func xrpTransfer(hash, from, to string, drops int64, tag *uint32) types.DetectedTransfer {
	return types.DetectedTransfer{
		TxHash: hash, BlockNumber: xrpTaggedLedger, BlockHash: "28D45A55AB2C0C4EC4F4B6F305D75DE861FD4F675301EC1035622910CD14FB2D",
		From: from, To: to, Amount: big.NewInt(drops), Asset: models.NativeXRP, DestinationTag: tag,
	}
}

// xrpWalletAccounts is the publisher's wallet port: the wallets the test inserted.
type xrpWalletAccounts map[uuid.UUID]*models.Wallet

func (w xrpWalletAccounts) FindByID(_ context.Context, id uuid.UUID) (*models.Wallet, error) {
	return w[id], nil
}

type xrpDepositFixture struct {
	svc     *Service
	adapter *mocks.MockChain
	wallet  models.Wallet
}

// newXRPDepositFixture wires the deposit service with the production deposit event
// publisher and a webhook config of the wallet's account subscribed to
// deposit.pending and deposit.confirmed.
func newXRPDepositFixture(t *testing.T, transfers ...types.DetectedTransfer) xrpDepositFixture {
	t.Helper()
	fixtures.TestDB(t)
	adapter := xrpLedgerChain(xrpTaggedLedger, transfers...)
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	account := fixtures.InsertAccount(t, "xrp deposit owner")
	wallet := fixtures.InsertWalletWithAccount(t, models.ChainTXRP, &account.ID)
	fixtures.InsertScopedWebhookConfig(t, "https://owner.test/xrp", xrpDepositHookSecret,
		[]string{string(types.EventDepositPending), string(types.EventDepositConfirmed)}, &account.ID, nil)

	webhookSvc := newWebhookSvc()
	svc := newDepositSvc(registry, webhookSvc)
	svc.SetDepositEvents(depositevents.NewPublisher(depositevents.PublisherDeps{
		Enqueuer: webhookSvc,
		Wallets:  xrpWalletAccounts{wallet.ID: &wallet},
		Decimals: chainAssetDecimals{models.ChainTXRP + "/" + models.NativeXRP: xrpDropsDecimals},
	}))
	return xrpDepositFixture{svc: svc, adapter: adapter, wallet: wallet}
}

func (f xrpDepositFixture) scanLedger(t *testing.T) {
	t.Helper()
	recorded, err := f.svc.ScanBlock(context.Background(), models.ChainTXRP, xrpTaggedLedger)
	if err != nil {
		t.Fatal(err)
	}
	if recorded == 0 {
		t.Fatal("the ledger recorded no deposit")
	}
}

func xrpDeposit(t *testing.T, hash string) models.Transaction {
	t.Helper()
	var deposit models.Transaction
	err := facades.Orm().Query().Where("chain", models.ChainTXRP).Where("tx_type", models.TxTypeDeposit).
		Where("tx_hash", hash).FirstOrFail(&deposit)
	if err != nil {
		t.Fatalf("deposit %s: %v", hash, err)
	}
	return deposit
}

// xrpDepositEvent is the data object of the one eventType webhook of transactionID,
// as the receiver gets it.
func xrpDepositEvent(t *testing.T, eventType types.EventType, transactionID uuid.UUID) map[string]any {
	t.Helper()
	var rows []models.WebhookEvent
	if err := facades.Orm().Query().Where("event_type = ? AND subject_id = ?", string(eventType), transactionID.String()).Find(&rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d %s webhooks for %s, want 1", len(rows), eventType, transactionID)
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(rows[0].Payload), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func TestXRP_Deposit_DestinationTagIsStoredAndSentInTheDepositWebhooks(t *testing.T) {
	tag := xrpTaggedTag
	f := newXRPDepositFixture(t,
		xrpTransfer(xrpTaggedHash, xrpTaggedSender, xrpTaggedRecipient, xrpTaggedDrops, &tag),
		xrpTransfer(xrpUntaggedHash, xrpUntaggedSender, xrpUntaggedRecipient, xrpUntaggedDrops, nil),
	)
	tagged := fixtures.InsertAddress(t, f.wallet.ID, models.ChainTXRP, xrpTaggedRecipient, "user_tagged", 1)
	fixtures.InsertAddress(t, f.wallet.ID, models.ChainTXRP, xrpUntaggedRecipient, "user_plain", 2)

	f.scanLedger(t)

	deposit := xrpDeposit(t, xrpTaggedHash)
	if deposit.DestinationTag == nil || *deposit.DestinationTag != xrpTaggedTag {
		t.Fatalf("stored destination tag %v, want %d", deposit.DestinationTag, xrpTaggedTag)
	}
	if deposit.AddressID == nil || *deposit.AddressID != tagged.ID || deposit.ExternalUserID != "user_tagged" ||
		deposit.Amount != "1000000" || deposit.Asset != models.NativeXRP || deposit.FromAddress != xrpTaggedSender ||
		deposit.BlockNumber != xrpTaggedLedger || deposit.RequiredConfs != 1 || deposit.Status != string(types.TxStatusPending) {
		t.Fatalf("deposit %+v", deposit)
	}
	plain := xrpDeposit(t, xrpUntaggedHash)
	if plain.DestinationTag != nil {
		t.Fatalf("a payment without a tag stored tag %d", *plain.DestinationTag)
	}

	// The validated ledger is the tip: tip - ledger + 1 = 1 confirmation, which is
	// all txrp requires.
	if err := f.svc.updateConfirmations(context.Background(), models.ChainTXRP, f.adapter, xrpTaggedLedger); err != nil {
		t.Fatal(err)
	}
	confirmed := xrpDeposit(t, xrpTaggedHash)
	if confirmed.Status != string(types.TxStatusConfirmed) || confirmed.Confirmations != 1 {
		t.Fatalf("deposit %s with %d confirmations, want confirmed with 1", confirmed.Status, confirmed.Confirmations)
	}

	for _, eventType := range []types.EventType{types.EventDepositPending, types.EventDepositConfirmed} {
		data := xrpDepositEvent(t, eventType, deposit.ID)
		if data["destination_tag"] != float64(xrpTaggedTag) {
			t.Fatalf("%s destination_tag = %v, want %d", eventType, data["destination_tag"], xrpTaggedTag)
		}
		if data["amount"] != "1" || data["amount_base_units"] != "1000000" || data["decimals"] != float64(xrpDropsDecimals) ||
			data["to_address"] != xrpTaggedRecipient || data["tx_hash"] != xrpTaggedHash {
			t.Fatalf("%s payload %v", eventType, data)
		}
		untagged := xrpDepositEvent(t, eventType, plain.ID)
		if _, present := untagged["destination_tag"]; present {
			t.Fatalf("%s of a payment without a tag carries destination_tag %v", eventType, untagged["destination_tag"])
		}
	}
}

// An XRP withdrawal leaves the wallet's base address for its destination; the ledger
// shows it as a Payment to that destination only, so the scan records nothing for
// the wallet, while a payment into the base address is a deposit.
func TestXRP_Deposit_AWithdrawalOfTheWalletIsNotADeposit(t *testing.T) {
	withdrawalTag, depositTag := uint32(42), xrpTaggedTag
	const withdrawalHash = "B01DD89B59CD557D61C826C5BC2E3DC85C245BFEB65E9D63656D376B2F31333F"
	f := newXRPDepositFixture(t)
	base := f.wallet.DepositAddress.Address
	f.adapter.ScanBlockFn = func(context.Context, uint64) ([]types.DetectedTransfer, error) {
		return []types.DetectedTransfer{
			xrpTransfer(withdrawalHash, base, xrpUntaggedRecipient, 3_000_000, &withdrawalTag),
			xrpTransfer(xrpTaggedHash, xrpTaggedSender, base, xrpTaggedDrops, &depositTag),
		}, nil
	}
	withdrawal := fixtures.InsertTransaction(t, f.wallet.ID, nil, models.ChainTXRP, models.TxTypeWithdrawal,
		string(types.TxStatusConfirming), models.NativeXRP, "3000000", 0)
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", withdrawal.ID).
		Update(map[string]any{"tx_hash": withdrawalHash, "to_address": xrpUntaggedRecipient, "destination_tag": withdrawalTag}); err != nil {
		t.Fatal(err)
	}

	f.scanLedger(t)

	var deposits []models.Transaction
	if err := facades.Orm().Query().Where("chain", models.ChainTXRP).Where("tx_type", models.TxTypeDeposit).Find(&deposits); err != nil {
		t.Fatal(err)
	}
	if len(deposits) != 1 || deposits[0].TxHash != xrpTaggedHash || deposits[0].ToAddress != base ||
		deposits[0].DestinationTag == nil || *deposits[0].DestinationTag != depositTag {
		t.Fatalf("deposits %+v, want only the tagged payment into the base address", deposits)
	}
	var reloaded models.Transaction
	if err := facades.Orm().Query().Where("id = ?", withdrawal.ID).FirstOrFail(&reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.TxType != models.TxTypeWithdrawal || reloaded.Status != string(types.TxStatusConfirming) {
		t.Fatalf("the withdrawal row changed: %+v", reloaded)
	}
}
