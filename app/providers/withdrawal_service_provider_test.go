package providers

import (
	"testing"

	"github.com/goravel/framework/foundation"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/repositories"
)

func TestWithdrawal_Provider_RegistersTheWithdrawalGraph(t *testing.T) {
	require.NotNil(t, foundation.App)
	(&WithdrawalServiceProvider{}).Register(foundation.App)

	transactions, err := container.Make[*repositories.TransactionRepository]()
	require.NoError(t, err)
	require.NotNil(t, transactions)

	withdrawals, err := container.Make[*repositories.WithdrawalRepository]()
	require.NoError(t, err)
	require.NotNil(t, withdrawals)

	require.Same(t, transactions, container.MustMake[*repositories.TransactionRepository]())
	require.Same(t, withdrawals, container.MustMake[*repositories.WithdrawalRepository]())
}
