package walletview

import (
	"context"
	"errors"
	"log/slog"

	"github.com/macrowallets/waas/app/models"
)

// ErrTransactionNotFound is the answer for a transaction the wallet does not hold.
var ErrTransactionNotFound = errors.New("transaction not found")

// TransactionsInput is a page of a wallet's transactions, optionally of one
// type and status.
type TransactionsInput struct {
	Wallet *models.Wallet
	Type   string
	Status string
	Limit  int
	Offset int
}

// TransactionPage is a page of transactions with the chain and tokens their
// asset decimals are read from. Either may be missing, and then those decimals
// are unknown.
type TransactionPage struct {
	Transactions []models.Transaction
	Total        int64
	Chain        *models.Chain
	Tokens       []models.Token
}

// TransactionsOf returns a page of the wallet's transactions.
func (s *Service) TransactionsOf(ctx context.Context, in TransactionsInput) (TransactionPage, error) {
	transactions, total, err := s.txs.FindByWallet(ctx, in.Wallet.ID, in.Type, in.Status, in.Limit, in.Offset)
	if err != nil {
		slog.Error("list wallet transactions", "wallet", in.Wallet.ID, "error", err)
		return TransactionPage{}, fetchFailed("transactions", err)
	}
	chain, tokens := s.assetCatalog(ctx, in.Wallet.Chain)
	return TransactionPage{Transactions: transactions, Total: total, Chain: chain, Tokens: tokens}, nil
}

// Transaction returns one transaction of the wallet. A transaction the wallet
// does not hold, and a lookup that fails, are ErrTransactionNotFound.
func (s *Service) Transaction(ctx context.Context, wallet *models.Wallet, txID string) (TransactionPage, error) {
	transaction, err := s.txs.FindByIDAndWallet(ctx, txID, wallet.ID)
	if err != nil || transaction == nil {
		return TransactionPage{}, ErrTransactionNotFound
	}
	chain, tokens := s.assetCatalog(ctx, wallet.Chain)
	return TransactionPage{Transactions: []models.Transaction{*transaction}, Total: 1, Chain: chain, Tokens: tokens}, nil
}

// assetCatalog reads the chain and its active tokens for the transaction
// decimals; a failed read leaves those decimals unknown instead of failing the
// response.
func (s *Service) assetCatalog(ctx context.Context, chainID string) (*models.Chain, []models.Token) {
	chainRecord, chainErr := s.chains.FindByID(ctx, chainID)
	if errors.Is(chainErr, models.ErrRepositoryNotFound) {
		chainRecord, chainErr = nil, nil
	}
	if chainErr != nil {
		slog.Warn("load chain for transaction decimals", "chain", chainID, "error", chainErr)
		chainRecord = nil
	}
	tokens, tokenErr := s.chains.FindTokens(ctx, chainID)
	if tokenErr != nil {
		slog.Warn("load tokens for transaction decimals", "chain", chainID, "error", tokenErr)
		tokens = nil
	}
	return chainRecord, tokens
}
