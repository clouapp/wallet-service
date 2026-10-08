package providers

import (
	"testing"

	"github.com/goravel/framework/foundation"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

func TestIdentityProvider_RegistersTheAccountGraph(t *testing.T) {
	require.NotNil(t, foundation.App)
	(&IdentityServiceProvider{}).Register(foundation.App)
	// The account service resolves the activity log and the platform-admin
	// lookup registered by production boot. This test's app is only the
	// providers it registers itself.
	(&ActivityServiceProvider{}).Register(foundation.App)
	(&FeaturesServiceProvider{}).Register(foundation.App)

	users, err := container.Make[*repositories.UserRepository]()
	require.NoError(t, err)
	require.NotNil(t, users)

	accounts, err := container.Make[*repositories.AccountRepository]()
	require.NoError(t, err)
	require.NotNil(t, accounts)

	memberships, err := container.Make[*repositories.AccountUserRepository]()
	require.NoError(t, err)
	require.NotNil(t, memberships)

	tokens, err := container.Make[*repositories.AccessTokenRepository]()
	require.NoError(t, err)
	require.NotNil(t, tokens)

	refresh, err := container.Make[*repositories.RefreshTokenRepository]()
	require.NoError(t, err)
	require.NotNil(t, refresh)

	resets, err := container.Make[*repositories.PasswordResetTokenRepository]()
	require.NoError(t, err)
	require.NotNil(t, resets)

	codes, err := container.Make[*repositories.TotpRecoveryCodeRepository]()
	require.NoError(t, err)
	require.NotNil(t, codes)

	svc, err := container.Make[*account.Service]()
	require.NoError(t, err)
	require.NotNil(t, svc)

	passwords, err := container.Make[*authsvc.Service]()
	require.NoError(t, err)
	require.NotNil(t, passwords)

	require.Same(t, users, container.MustMake[*repositories.UserRepository]())
}
