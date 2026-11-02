package deposit

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
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

// A Bitcoin withdrawal pays its destination and sends the change back to the address it
// spent from, in the same transaction (the production smoke test of 2026-10-30: 3,314 sats
// in, 2,000 to the destination, 890 back to the base address, 424 of fee).
const (
	changeTx       = "fd9014e4c5b289d374e42616d6e76cd6dcb96ccb1ad1370f39a03b116ab84f34"
	changeBase     = "bc1qbase"
	changeChild    = "bc1qchild"
	changeExternal = "bc1qexternal"
)

// recordWithdrawal stores the row the withdrawal flow writes before it broadcasts.
func recordWithdrawal(t *testing.T, walletID uuid.UUID, from, to string, amount string) {
	t.Helper()
	tx := models.Transaction{
		ID: uuid.New(), WalletID: walletID, ExternalUserID: "user-" + from,
		Chain: models.ChainBTC, TxType: models.TxTypeWithdrawal, TxHash: changeTx, LogIndex: models.TransactionLogIndexNone,
		FromAddress: from, ToAddress: to, Amount: amount, Asset: models.NativeBTC,
		RequiredConfs: 6, Status: string(types.TxStatusConfirming),
		Direction: models.TxDirectionOutbound, Source: models.TxSourceWithdrawalFlow, RawPayload: "{}",
	}
	if err := facades.Orm().Query().Create(&tx); err != nil {
		t.Fatalf("insert withdrawal: %v", err)
	}
}

func changeOutput(to string, sats int64, vout int) types.DetectedTransfer {
	return types.DetectedTransfer{TxHash: changeTx, BlockNumber: positionsBlock, BlockHash: "00000000block",
		From: changeBase, To: to, Amount: big.NewInt(sats), Asset: models.NativeBTC, LogIndex: vout}
}

func pushed(output types.DetectedTransfer) providers.InboundTransfer {
	return providers.InboundTransfer{TxHash: output.TxHash, BlockNumber: output.BlockNumber, BlockHash: output.BlockHash,
		From: output.From, To: output.To, Amount: output.Amount, Asset: output.Asset, LogIndex: output.LogIndex}
}

// record runs one source over the block: the scanner, or the provider push of the
// outputs to watched addresses.
func (f *positionsFixture) record(t *testing.T, source string, watched ...string) {
	t.Helper()
	if source == "scanner" {
		f.scan(t, models.ChainBTC, positionsBlock)
		return
	}
	isWatched := map[string]bool{}
	for _, address := range watched {
		isWatched[address] = true
	}
	var push []providers.InboundTransfer
	for _, output := range f.blocks[positionsBlock] {
		if isWatched[output.To] {
			push = append(push, pushed(output))
		}
	}
	require.NoError(t, f.newIngestService().Ingest(context.Background(), ingest.InboundEvent{ChainID: models.ChainBTC, Transfers: push}))
}

func TestScan_And_Push_TheChangeOfAWithdrawalIsNotADeposit(t *testing.T) {
	for _, source := range []string{"scanner", "push"} {
		t.Run(source, func(t *testing.T) {
			f := newPositionsFixture(t, models.ChainBTC, models.NativeBTC)
			base := f.watch(t, models.ChainBTC, changeBase)
			recordWithdrawal(t, base.WalletID, base.Address, changeExternal, "2000")
			f.blocks[positionsBlock] = []types.DetectedTransfer{
				changeOutput(changeExternal, 2_000, 0),
				changeOutput(base.Address, 890, 1),
			}

			f.record(t, source, base.Address)

			assert.Empty(t, depositsOf(t, models.ChainBTC, changeTx), "the change is the wallet's own money coming back")
			assert.Empty(t, f.events.pending())
		})
	}
}

func TestScan_And_Push_AWithdrawalToAWatchedAddressIsADepositButNotItsChange(t *testing.T) {
	for _, source := range []string{"scanner", "push"} {
		t.Run(source, func(t *testing.T) {
			f := newPositionsFixture(t, models.ChainBTC, models.NativeBTC)
			base := f.watch(t, models.ChainBTC, changeBase)
			child := fixtures.InsertAddress(t, base.WalletID, models.ChainBTC, changeChild, "user-child", 1)
			recordWithdrawal(t, base.WalletID, base.Address, child.Address, "2000")
			f.blocks[positionsBlock] = []types.DetectedTransfer{
				changeOutput(child.Address, 2_000, 0),
				changeOutput(base.Address, 890, 1),
			}

			f.record(t, source, base.Address, child.Address)

			rows := depositsOf(t, models.ChainBTC, changeTx)
			require.Len(t, rows, 1)
			assert.Equal(t, child.Address, rows[0].ToAddress)
			assert.Equal(t, "2000", rows[0].Amount)
			assert.Equal(t, 0, rows[0].LogIndex)
		})
	}
}

// An address that withdraws to itself gets its payment and its change back in two
// outputs that cannot be told apart by address: both stay deposits, as before.
func TestScan_Block_AWithdrawalAnAddressSendsToItselfKeepsItsDeposits(t *testing.T) {
	f := newPositionsFixture(t, models.ChainBTC, models.NativeBTC)
	base := f.watch(t, models.ChainBTC, changeBase)
	recordWithdrawal(t, base.WalletID, base.Address, base.Address, "2000")
	f.blocks[positionsBlock] = []types.DetectedTransfer{
		changeOutput(base.Address, 2_000, 0),
		changeOutput(base.Address, 890, 1),
	}

	assert.Equal(t, 2, f.scan(t, models.ChainBTC, positionsBlock))
}
