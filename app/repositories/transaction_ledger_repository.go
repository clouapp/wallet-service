package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// ledgerScope picks the rows a ledger sums beside their chain: those whose address
// column (to_address for the legs in, from_address for the legs out) is one of a
// list, or those of one wallet (transactions_wallet_id_index).
type ledgerScope struct {
	where func(addressColumn string) string
	arg   any
}

func addressesScope(addresses []string) ledgerScope {
	return ledgerScope{where: func(column string) string { return column + " IN ?" }, arg: addresses}
}

func walletScope(walletID uuid.UUID) ledgerScope {
	return ledgerScope{where: func(string) string { return "wallet_id = ?" }, arg: walletID}
}

// AddressLedger sums, for each of addresses on chainID and each asset, the confirmed
// amounts the recorded rows moved in (deposits, sweeps, gas seeds to it) and out
// (withdrawals, sweeps, gas seeds from it), and counts those still in flight. A
// deposit row's sender and a withdrawal row's recipient are left out: the other
// side's row (the withdrawal, the deposit) is the one that moved them.
func (r *TransactionRepository) AddressLedger(ctx context.Context, chainID string, addresses []string) ([]models.AddressLedgerEntry, error) {
	if len(addresses) == 0 {
		return nil, nil
	}
	return r.addressLedger(ctx, chainID, addressesScope(addresses))
}

// WalletAddressLedger is AddressLedger for every address the rows of one wallet on
// chainID moved funds to or from: its base address and its other deposit addresses.
func (r *TransactionRepository) WalletAddressLedger(ctx context.Context, walletID uuid.UUID, chainID string) ([]models.AddressLedgerEntry, error) {
	return r.addressLedger(ctx, chainID, walletScope(walletID))
}

func (r *TransactionRepository) addressLedger(ctx context.Context, chainID string, scope ledgerScope) ([]models.AddressLedgerEntry, error) {
	var rows []models.AddressLedgerEntry
	if err := r.Query(ctx).Raw(
		`SELECT address, MIN(asset) AS asset, token_contract,
		        COALESCE(SUM(amount::numeric) FILTER (WHERE leg = 'in' AND status = ?), 0)::text AS credited,
		        COALESCE(SUM(amount::numeric) FILTER (WHERE leg = 'out' AND status = ?), 0)::text AS debited,
		        COUNT(*) FILTER (WHERE status IN (?, ?)) AS in_flight
		 FROM (
		   SELECT to_address AS address, 'in' AS leg, asset, COALESCE(token_contract, '') AS token_contract, amount, status
		   FROM transactions WHERE chain = ? AND `+scope.where("to_address")+` AND tx_type IN (?, ?, ?)
		   UNION ALL
		   SELECT from_address, 'out', asset, COALESCE(token_contract, ''), amount, status
		   FROM transactions WHERE chain = ? AND `+scope.where("from_address")+` AND tx_type IN (?, ?, ?)
		 ) legs
		 GROUP BY address, token_contract
		 ORDER BY address, token_contract`,
		string(types.TxStatusConfirmed), string(types.TxStatusConfirmed),
		string(types.TxStatusPending), string(types.TxStatusConfirming),
		chainID, scope.arg, models.TxTypeDeposit, models.TxTypeSweep, models.TxTypeGasSeed,
		chainID, scope.arg, models.TxTypeWithdrawal, models.TxTypeSweep, models.TxTypeGasSeed,
	).Scan(&rows); err != nil {
		return nil, fmt.Errorf("sum address ledger: %w", err)
	}
	return rows, nil
}

// AddressFees sums, for each of addresses on chainID, what its withdrawals, sweeps
// and gas seeds paid beside the amounts they moved: the recorded fee of the
// confirmed rows and of the rows failed on chain, plus the account rent recorded in
// raw_payload. It counts the confirmed rows, and those mined and failed (block > 0),
// without a fee; with withAccountRent, the confirmed rows without an account rent;
// and the rows in flight.
func (r *TransactionRepository) AddressFees(ctx context.Context, chainID string, addresses []string, withAccountRent bool) ([]models.AddressFeeEntry, error) {
	if len(addresses) == 0 {
		return nil, nil
	}
	return r.addressFees(ctx, chainID, addressesScope(addresses), withAccountRent)
}

// WalletAddressFees is AddressFees for every address the outbound rows of one wallet
// on chainID were sent from.
func (r *TransactionRepository) WalletAddressFees(ctx context.Context, walletID uuid.UUID, chainID string, withAccountRent bool) ([]models.AddressFeeEntry, error) {
	return r.addressFees(ctx, chainID, walletScope(walletID), withAccountRent)
}

func (r *TransactionRepository) addressFees(ctx context.Context, chainID string, scope ledgerScope, withAccountRent bool) ([]models.AddressFeeEntry, error) {
	var rows []models.AddressFeeEntry
	if err := r.Query(ctx).Raw(
		`SELECT from_address AS address,
		        COALESCE(SUM(COALESCE(NULLIF(fee, '')::numeric, 0) + COALESCE(NULLIF(raw_payload ->> ?, '')::numeric, 0))
		                 FILTER (WHERE status IN (?, ?)), 0)::text AS fees,
		        COUNT(*) FILTER (WHERE COALESCE(fee, '') = '' AND (status = ? OR (status = ? AND block_number > 0))) AS missing_fees,
		        CASE WHEN ? THEN COUNT(*) FILTER (WHERE status = ? AND (raw_payload ->> ?) IS NULL) ELSE 0 END AS missing_rent,
		        COUNT(*) FILTER (WHERE status IN (?, ?)) AS in_flight
		 FROM transactions
		 WHERE chain = ? AND `+scope.where("from_address")+` AND tx_type IN (?, ?, ?)
		 GROUP BY from_address
		 ORDER BY from_address`,
		chain.AccountRentPayloadKey,
		string(types.TxStatusConfirmed), string(types.TxStatusFailed),
		string(types.TxStatusConfirmed), string(types.TxStatusFailed),
		withAccountRent, string(types.TxStatusConfirmed), chain.AccountRentPayloadKey,
		string(types.TxStatusPending), string(types.TxStatusConfirming),
		chainID, scope.arg, models.TxTypeWithdrawal, models.TxTypeSweep, models.TxTypeGasSeed,
	).Scan(&rows); err != nil {
		return nil, fmt.Errorf("sum address fees: %w", err)
	}
	return rows, nil
}

// FindConfirmedOutboundWithoutAccountRent lists, oldest first, the confirmed
// withdrawals, sweeps and gas seeds of chainID whose raw_payload has no account rent
// (chain.AccountRentPayloadKey), at most limit.
func (r *TransactionRepository) FindConfirmedOutboundWithoutAccountRent(ctx context.Context, chainID string, limit int) ([]models.Transaction, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("list transactions without account rent: limit is required")
	}
	var rows []models.Transaction
	err := r.Query(ctx).Raw(
		`SELECT * FROM transactions
		 WHERE chain = ? AND tx_type IN (?, ?, ?) AND status = ? AND tx_hash <> '' AND (raw_payload ->> ?) IS NULL
		 ORDER BY created_at, id
		 LIMIT ?`,
		chainID, models.TxTypeWithdrawal, models.TxTypeSweep, models.TxTypeGasSeed, string(types.TxStatusConfirmed),
		chain.AccountRentPayloadKey, limit,
	).Scan(&rows)
	if err != nil {
		return nil, fmt.Errorf("list transactions without account rent: %w", err)
	}
	return rows, nil
}
