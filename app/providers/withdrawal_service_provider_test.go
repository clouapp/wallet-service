package providers

import (
	"testing"

	"github.com/goravel/framework/foundation"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/stretchr/testify/assert"
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

	assert.Same(t, transactions, container.MustMake[*repositories.TransactionRepository]())
	assert.Same(t, withdrawals, container.MustMake[*repositories.WithdrawalRepository]())
}
