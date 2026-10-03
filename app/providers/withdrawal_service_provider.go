package providers

import (
	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WithdrawalServiceProvider binds the transaction and withdrawal repositories
// by type. Sweep keeps a narrow transactionWriter for the row it persists
// after broadcast; signing stays outside this provider.
type WithdrawalServiceProvider struct{}

func (p *WithdrawalServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.TransactionRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewTransactionRepository(nil), nil
	})
	app.Singleton((*repositories.WithdrawalRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWithdrawalRepository(nil), nil
	})
	app.Singleton((*walletrecords.Transactions)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.TransactionRepository](app)
		if err != nil {
			return nil, err
		}
		return walletrecords.NewTransactions(store), nil
	})
}

func (p *WithdrawalServiceProvider) Boot(foundation.Application) {}
