package repositories_test

import (
	"context"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// ledgerRow inserts one row of txType from → to on teth; contract "" is native and
// fee "" records none.
func (s *TransactionRepositoryTestSuite) ledgerRow(walletID uuid.UUID, txType, status, from, to, asset, contract, amount, fee string) {
	tx := s.makeTx(walletID, txType, status)
	tx.Chain = models.ChainTETH
	tx.FromAddress, tx.ToAddress = from, to
	tx.Asset, tx.TokenContract, tx.Amount, tx.Fee = asset, contract, amount, fee
	s.Require().NoError(s.repo.Create(context.Background(), tx))
}

const (
	ledgerBase  = "0xbase000000000000000000000000000000000001"
	ledgerChild = "0xchild00000000000000000000000000000000002"
	ledgerUSDC  = "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238"
	ledgerOther = "0xoutside0000000000000000000000000000000003"
)

func (s *TransactionRepositoryTestSuite) TestAddress_Ledger_SumsWhatTheRecordedRowsMovedInAndOutOfEachAddress() {
	ctx := context.Background()
	walletID := s.insertWallet()
	// The child: two confirmed deposits, one failed on chain, one still confirming,
	// a gas seed from the base, and a USDC deposit swept to the base.
	s.ledgerRow(walletID, models.TxTypeDeposit, "confirmed", ledgerOther, ledgerChild, "ETH", "", "1000", "")
	s.ledgerRow(walletID, models.TxTypeDeposit, "confirmed", ledgerOther, ledgerChild, "ETH", "", "500", "")
	s.ledgerRow(walletID, models.TxTypeDeposit, "failed", ledgerOther, ledgerChild, "ETH", "", "9999", "")
	s.ledgerRow(walletID, models.TxTypeDeposit, "confirming", ledgerOther, ledgerChild, "ETH", "", "7", "")
	s.ledgerRow(walletID, models.TxTypeGasSeed, "confirmed", ledgerBase, ledgerChild, "ETH", "", "300", "21")
	s.ledgerRow(walletID, models.TxTypeDeposit, "confirmed", ledgerOther, ledgerChild, "USDC", ledgerUSDC, "2500000", "")
	s.ledgerRow(walletID, models.TxTypeSweep, "confirmed", ledgerChild, ledgerBase, "USDC", ledgerUSDC, "2500000", "250")
	// The base: a withdrawal out (paid fee), one that failed on chain (fee burned),
	// one refused before it left (no fee) and one whose fee was never recorded.
	s.ledgerRow(walletID, models.TxTypeWithdrawal, "confirmed", ledgerBase, ledgerOther, "ETH", "", "100", "30")
	s.ledgerRow(walletID, models.TxTypeWithdrawal, "failed", ledgerBase, ledgerOther, "ETH", "", "100", "12")
	s.ledgerRow(walletID, models.TxTypeWithdrawal, "failed", ledgerBase, ledgerOther, "ETH", "", "100", "")
	s.ledgerRow(walletID, models.TxTypeWithdrawal, "confirmed", ledgerBase, ledgerOther, "ETH", "", "40", "")
	// One mined and failed on chain whose burned fee was never recorded.
	minedFailed := s.makeTx(walletID, models.TxTypeWithdrawal, "failed")
	minedFailed.Chain, minedFailed.FromAddress, minedFailed.ToAddress, minedFailed.Asset, minedFailed.BlockNumber = models.ChainTETH, ledgerBase, ledgerOther, "ETH", 500
	s.Require().NoError(s.repo.Create(ctx, minedFailed))
	// A deposit row whose sender is the base (a withdrawal to a watched address of
	// another wallet) is that wallet's credit, never the base's debit.
	s.ledgerRow(walletID, models.TxTypeDeposit, "confirmed", ledgerBase, ledgerOther, "ETH", "", "100", "")
	// Another chain's row never counts.
	other := s.makeTx(walletID, models.TxTypeDeposit, "confirmed")
	other.Chain, other.ToAddress, other.Amount = models.ChainTPolygon, ledgerChild, "123456"
	s.Require().NoError(s.repo.Create(ctx, other))
	nullContract := s.makeTx(walletID, models.TxTypeDeposit, "confirmed")
	nullContract.Chain, nullContract.ToAddress, nullContract.Asset, nullContract.Amount = models.ChainTETH, ledgerChild, "ETH", "1"
	s.Require().NoError(s.repo.Create(ctx, nullContract))
	_, err := facades.Orm().Query().Exec(`UPDATE transactions SET token_contract = NULL WHERE id = ?`, nullContract.ID)
	s.Require().NoError(err)

	entries, err := s.repo.AddressLedger(ctx, models.ChainTETH, []string{ledgerBase, ledgerChild})
	s.Require().NoError(err)

	byKey := map[string]models.AddressLedgerEntry{}
	for _, entry := range entries {
		byKey[entry.Address+"/"+entry.TokenContract] = entry
	}
	s.Len(entries, 4, "per address and asset: base ETH, base USDC, child ETH, child USDC")
	s.Equal(models.AddressLedgerEntry{Address: ledgerChild, Asset: "ETH", Credited: "1801", Debited: "0", InFlight: 1}, byKey[ledgerChild+"/"])
	s.Equal(models.AddressLedgerEntry{Address: ledgerChild, Asset: "USDC", TokenContract: ledgerUSDC, Credited: "2500000", Debited: "2500000"}, byKey[ledgerChild+"/"+ledgerUSDC])
	s.Equal(models.AddressLedgerEntry{Address: ledgerBase, Asset: "ETH", Credited: "0", Debited: "440"}, byKey[ledgerBase+"/"])
	s.Equal(models.AddressLedgerEntry{Address: ledgerBase, Asset: "USDC", TokenContract: ledgerUSDC, Credited: "2500000", Debited: "0"}, byKey[ledgerBase+"/"+ledgerUSDC])

	fees, err := s.repo.AddressFees(ctx, models.ChainTETH, []string{ledgerBase, ledgerChild}, false)
	s.Require().NoError(err)
	feesByAddress := map[string]models.AddressFeeEntry{}
	for _, entry := range fees {
		feesByAddress[entry.Address] = entry
	}
	s.Equal(models.AddressFeeEntry{Address: ledgerBase, Fees: "63", MissingFees: 2}, feesByAddress[ledgerBase],
		"the gas seed and the paid and burned withdrawal fees; the confirmed withdrawal without one and the one mined and failed without one are missing, the refused one paid none")
	s.Equal(models.AddressFeeEntry{Address: ledgerChild, Fees: "250"}, feesByAddress[ledgerChild], "the sweep's gas")
}

func (s *TransactionRepositoryTestSuite) TestAddress_Ledger_CountsTheOutboundRowsStillInFlightOfEachAddress() {
	ctx := context.Background()
	walletID := s.insertWallet()
	s.ledgerRow(walletID, models.TxTypeSweep, "pending", ledgerChild, ledgerBase, "USDC", ledgerUSDC, "5", "")
	s.ledgerRow(walletID, models.TxTypeWithdrawal, "confirming", ledgerBase, ledgerOther, "ETH", "", "5", "")

	fees, err := s.repo.AddressFees(ctx, models.ChainTETH, []string{ledgerBase, ledgerChild}, false)
	s.Require().NoError(err)
	s.ElementsMatch([]models.AddressFeeEntry{
		{Address: ledgerBase, Fees: "0", InFlight: 1},
		{Address: ledgerChild, Fees: "0", InFlight: 1},
	}, fees, "a pending token sweep still moves the child's native fee")

	none, err := s.repo.AddressLedger(ctx, models.ChainTETH, nil)
	s.Require().NoError(err)
	s.Empty(none)
}

func (s *TransactionRepositoryTestSuite) TestAddress_Fees_AddTheAccountRentAndCountTheRowsWithoutOne() {
	ctx := context.Background()
	walletID := s.insertWallet()
	rented := s.makeTx(walletID, models.TxTypeWithdrawal, "confirmed")
	rented.Chain, rented.FromAddress, rented.ToAddress, rented.Asset, rented.TokenContract, rented.Fee = models.ChainTSOL, "SoBase", "SoOther", "USDC", "4zMMC9", "5000"
	rented.RawPayload = `{"account_rent":"2039280"}`
	s.Require().NoError(s.repo.Create(ctx, rented))
	unrented := s.makeTx(walletID, models.TxTypeSweep, "confirmed")
	unrented.Chain, unrented.FromAddress, unrented.ToAddress, unrented.Asset, unrented.Fee = models.ChainTSOL, "SoBase", "SoOther", "SOL", "5000"
	s.Require().NoError(s.repo.Create(ctx, unrented))

	withRent, err := s.repo.AddressFees(ctx, models.ChainTSOL, []string{"SoBase"}, true)
	s.Require().NoError(err)
	s.Equal([]models.AddressFeeEntry{{Address: "SoBase", Fees: "2049280", MissingRent: 1}}, withRent,
		"both fees and the rent the withdrawal paid for the recipient's token account; the sweep has no rent recorded")

	withoutRent, err := s.repo.AddressFees(ctx, models.ChainTSOL, []string{"SoBase"}, false)
	s.Require().NoError(err)
	s.Equal([]models.AddressFeeEntry{{Address: "SoBase", Fees: "2049280"}}, withoutRent, "a chain that records no rent counts none missing")

	missing, err := s.repo.FindConfirmedOutboundWithoutAccountRent(ctx, models.ChainTSOL, 10)
	s.Require().NoError(err)
	s.Require().Len(missing, 1)
	s.Equal(unrented.ID, missing[0].ID)
}

func (s *AddressRepositoryTestSuite) TestList_ForBalanceAudit_PagesTheChainsAddressesByIDWithinAnAccount() {
	ctx := context.Background()
	account := fixtures.InsertAccount(s.T(), "audit")
	mine := fixtures.InsertWalletWithAccount(s.T(), models.ChainTETH, &account.ID)
	fixtures.InsertAddress(s.T(), mine.ID, models.ChainTETH, "0xmine-child-1", "user-1", 1)
	inactive := fixtures.InsertAddress(s.T(), mine.ID, models.ChainTETH, "0xmine-child-2", "user-2", 2)
	_, err := facades.Orm().Query().Exec(`UPDATE addresses SET is_active = false WHERE id = ?`, inactive.ID)
	s.Require().NoError(err)
	theirs := fixtures.InsertWallet(s.T(), models.ChainTETH)
	fixtures.InsertAddress(s.T(), theirs.ID, models.ChainTETH, "0xtheirs-child", "user-3", 1)
	polygon := fixtures.InsertWalletWithAccount(s.T(), models.ChainTPolygon, &account.ID)
	fixtures.InsertAddress(s.T(), polygon.ID, models.ChainTPolygon, "0xpolygon-child", "user-4", 1)

	all, err := s.repo.ListForBalanceAudit(ctx, models.ChainTETH, nil, uuid.Nil, 10)
	s.Require().NoError(err)
	s.Len(all, 5, "every teth address, inactive ones included: two bases and three children")
	for i := 1; i < len(all); i++ {
		s.Less(all[i-1].ID.String(), all[i].ID.String(), "ordered by id")
	}

	scoped, err := s.repo.ListForBalanceAudit(ctx, models.ChainTETH, &account.ID, uuid.Nil, 10)
	s.Require().NoError(err)
	s.Len(scoped, 3, "the account's base and its two children")
	first, err := s.repo.ListForBalanceAudit(ctx, models.ChainTETH, &account.ID, uuid.Nil, 2)
	s.Require().NoError(err)
	rest, err := s.repo.ListForBalanceAudit(ctx, models.ChainTETH, &account.ID, first[1].ID, 2)
	s.Require().NoError(err)
	s.Require().Len(rest, 1)
	s.Equal(scoped[2].ID, rest[0].ID, "the next page starts after the last id read")

	_, err = s.repo.ListForBalanceAudit(ctx, models.ChainTETH, nil, uuid.Nil, 0)
	s.Error(err, "a limit is required")
}
