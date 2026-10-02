package providers

import (
	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/repositories"
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
}

func (p *WithdrawalServiceProvider) Boot(foundation.Application) {}
