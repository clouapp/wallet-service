package deposit

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const internalTransferBlock = 900

// recordOutbound stores the row the sweep executor writes right after broadcasting.
func recordOutbound(t *testing.T, walletID uuid.UUID, txType, txHash, to string) {
	t.Helper()
	tx := models.Transaction{
		ID:             uuid.New(),
		WalletID:       walletID,
		ExternalUserID: "user_scan",
		Chain:          scanTestChain,
		TxType:         txType,
		TxHash:         txHash,
		ToAddress:      to,
		Amount:         "1000",
		Asset:          scanTestChain,
		RequiredConfs:  1,
		Status:         string(types.TxStatusConfirming),
		Direction:      models.TxDirectionSelf,
		Source:         models.TxSourceWithdrawalFlow,
		RawPayload:     "{}",
	}
	if err := facades.Orm().Query().Create(&tx); err != nil {
		t.Fatalf("insert %s: %v", txType, err)
	}
}

func walletOfAddress(t *testing.T, address string) uuid.UUID {
	t.Helper()
	var addr models.Address
	err := facades.Orm().Query().Where("chain", scanTestChain).Where("address", address).First(&addr)
	if err != nil || addr.WalletID == uuid.Nil {
		t.Fatalf("watched address %s not found: %v", address, err)
	}
	return addr.WalletID
}

// The sweep executor records a sweep (child → base) and its gas seed (base → child)
// under the wallet; the scanner then sees the same transactions reach watched
// addresses and must not record them again as deposits. A withdrawal of the wallet that
// lands on a watched address, and a sweep recorded by another wallet, are deposits.
func TestScan_Block_SkipsSweepsAndGasSeedsOfTheSameWallet(t *testing.T) {
	f := newScanFixture(t, 1000, DefaultScanOptions())
	walletID := walletOfAddress(t, f.address)
	otherWallet := fixtures.InsertWallet(t, scanTestChain)

	recordOutbound(t, walletID, models.TxTypeSweep, "tx-sweep", f.address)
	recordOutbound(t, walletID, models.TxTypeGasSeed, "tx-gas-seed", f.address)
	recordOutbound(t, walletID, models.TxTypeWithdrawal, "tx-funding", f.address)
	recordOutbound(t, otherWallet.ID, models.TxTypeSweep, "tx-other-wallet", f.address)
	for _, hash := range []string{"tx-sweep", "tx-gas-seed", "tx-funding", "tx-other-wallet", "tx-external"} {
		f.adapter.depositTo(internalTransferBlock, hash, f.address)
	}

	recorded, err := f.svc.ScanBlock(context.Background(), scanTestChain, internalTransferBlock)
	if err != nil {
		t.Fatal(err)
	}
	if recorded != 3 {
		t.Fatalf("recorded %d deposits, want 3", recorded)
	}
	got := depositHashes(t)
	want := map[string]bool{"tx-funding": true, "tx-other-wallet": true, "tx-external": true}
	if len(got) != len(want) {
		t.Fatalf("deposits %s, want tx-funding, tx-other-wallet and tx-external", strings.Join(got, ","))
	}
	for _, hash := range got {
		if !want[hash] {
			t.Fatalf("recorded %s as a deposit; deposits %s", hash, strings.Join(got, ","))
		}
	}
	if n := f.events.count(types.EventDepositPending); n != 3 {
		t.Fatalf("expected 3 deposit.pending events, got %d", n)
	}

	var internal int64
	internal, err = facades.Orm().Query().Model(&models.Transaction{}).
		Where("chain", scanTestChain).
		Where("tx_type IN ?", []string{models.TxTypeSweep, models.TxTypeGasSeed}).
		Count()
	if err != nil {
		t.Fatal(err)
	}
	if internal != 3 {
		t.Fatalf("the sweep and gas seed rows must stay, got %d internal rows", internal)
	}
}
