package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// Every single-row lookup reports a missing row as the bare
// models.ErrRepositoryNotFound, the value callers compare against.
func TestFind_MissingRow_IsErrRepositoryNotFound(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	missing, other := uuid.New(), uuid.New()
	sealed := facades.Crypt()
	webhookConfigs := repositories.NewWebhookConfigRepository(repositories.WebhookConfigRepositoryDeps{Cipher: sealed})

	lookups := map[string]func() error{
		"account activity": func() error {
			_, err := repositories.NewAccountActivityRepository(nil).Find(ctx, missing, other)
			return err
		},
		"access token by account": func() error {
			_, err := repositories.NewAccessTokenRepository(nil).FindByIDAndAccount(ctx, missing, other)
			return err
		},
		"account": func() error { _, err := repositories.NewAccountRepository(nil).FindByID(ctx, missing); return err },
		"chain registry account": func() error {
			_, err := repositories.NewChainRegistryRepository(nil).FindAccount(ctx, missing)
			return err
		},
		"account user by id": func() error { _, err := repositories.NewAccountUserRepository(nil).FindByID(ctx, missing); return err },
		"account user by pair": func() error {
			_, err := repositories.NewAccountUserRepository(nil).FindByAccountAndUser(ctx, missing, other)
			return err
		},
		"account user by pair incl deleted": func() error {
			_, err := repositories.NewAccountUserRepository(nil).FindByAccountAndUserIncludeDeleted(ctx, missing, other)
			return err
		},
		"account user for owner attach": func() error {
			_, err := repositories.NewAccountUserRepository(nil).FindForOwnerAttach(ctx, missing, other)
			return err
		},
		"invite pending by email": func() error {
			_, err := repositories.NewAccountInviteRepository(nil).FindPendingByAccountEmail(ctx, missing, "no@example.com")
			return err
		},
		"invite open by account and id": func() error {
			_, err := repositories.NewAccountInviteRepository(nil).FindOpenByAccountAndID(ctx, missing, other)
			return err
		},
		"invite open by id": func() error {
			_, err := repositories.NewAccountInviteRepository(nil).FindOpenByID(ctx, missing)
			return err
		},
		"invite pending by token": func() error {
			_, err := repositories.NewAccountInviteRepository(nil).FindPendingByTokenHash(ctx, "nope", time.Now())
			return err
		},
		"address by chain and address": func() error {
			_, err := repositories.NewAddressRepository(nil).FindByChainAndAddress(ctx, "eth", "0xnone")
			return err
		},
		"address for account": func() error {
			_, err := repositories.NewAddressRepository(nil).FindByChainAndAddressAndAccount(ctx, "eth", "0xnone", missing)
			return err
		},
		"address by id": func() error { _, err := repositories.NewAddressRepository(nil).FindByID(ctx, missing); return err },
		"chain":         func() error { _, err := repositories.NewChainRepository(nil).FindByID(ctx, "nochain"); return err },
		"chain resource": func() error {
			_, err := repositories.NewChainResourceRepository(nil).FindByChainTypeAndName(ctx, "eth", "explorer", "none")
			return err
		},
		"currency":    func() error { _, err := repositories.NewCurrencyRepository(nil).FindByCode(ctx, "ZZZ"); return err },
		"token by id": func() error { _, err := repositories.NewTokenRepository(nil).FindByID(ctx, missing); return err },
		"token by contract": func() error {
			_, err := repositories.NewTokenRepository(nil).FindByChainAndContract(ctx, "eth", "0xnone")
			return err
		},
		"transaction by id": func() error { _, err := repositories.NewTransactionRepository(nil).FindByID(ctx, missing); return err },
		"transaction for account": func() error {
			_, err := repositories.NewTransactionRepository(nil).FindByIDForAccount(ctx, missing, other)
			return err
		},
		"transaction by wallet": func() error {
			_, err := repositories.NewTransactionRepository(nil).FindByIDAndWallet(ctx, missing.String(), other)
			return err
		},
		"transaction by idempotency key": func() error {
			_, err := repositories.NewTransactionRepository(nil).FindByIdempotencyKey(ctx, "none")
			return err
		},
		"transaction by hash": func() error {
			_, err := repositories.NewTransactionRepository(nil).FindByChainAndTxHash(ctx, "eth", "0xnone")
			return err
		},
		"user by email": func() error {
			_, err := repositories.NewUserRepository(nil).FindByEmail(ctx, "none@example.com")
			return err
		},
		"user by id": func() error { _, err := repositories.NewUserRepository(nil).FindByID(ctx, missing); return err },
		"wallet":     func() error { _, err := repositories.NewWalletRepository(nil).FindByID(ctx, missing); return err },
		"wallet for account": func() error {
			_, err := repositories.NewWalletRepository(nil).FindByIDAndAccount(ctx, missing, other)
			return err
		},
		"wallet sync state": func() error {
			_, err := repositories.NewWalletSyncStateRepository(nil).Find(ctx, missing, "eth", "balances")
			return err
		},
		"wallet user by id": func() error { _, err := repositories.NewWalletUserRepository(nil).FindByID(ctx, missing); return err },
		"wallet user by pair": func() error {
			_, err := repositories.NewWalletUserRepository(nil).FindByWalletAndUser(ctx, missing, other)
			return err
		},
		"wallet user by pair incl deleted": func() error {
			_, err := repositories.NewWalletUserRepository(nil).FindByWalletAndUserIncludeDeleted(ctx, missing, other)
			return err
		},
		"webhook config by id":     func() error { _, err := webhookConfigs.FindByID(ctx, missing); return err },
		"webhook config by wallet": func() error { _, err := webhookConfigs.FindByIDAndWallet(ctx, missing, other); return err },
		"webhook config ownership": func() error { _, err := webhookConfigs.FindOwnership(ctx, missing); return err },
		"webhook subscription by chain": func() error {
			_, err := repositories.NewWebhookSubscriptionRepository(nil).FindByChainID(ctx, "eth")
			return err
		},
		"webhook subscription by provider": func() error {
			_, err := repositories.NewWebhookSubscriptionRepository(nil).FindByProviderAndChain(ctx, "alchemy", "eth")
			return err
		},
		"whitelist entry": func() error {
			_, err := repositories.NewWhitelistEntryRepository(nil).FindByIDAndWallet(ctx, missing, other)
			return err
		},
		"withdrawal by id": func() error { _, err := repositories.NewWithdrawalRepository(nil).FindByID(ctx, missing); return err },
		"withdrawal by wallet": func() error {
			_, err := repositories.NewWithdrawalRepository(nil).FindByIDAndWallet(ctx, missing, other)
			return err
		},
		"withdrawal by transaction": func() error {
			_, err := repositories.NewWithdrawalRepository(nil).FindByTransactionID(ctx, missing)
			return err
		},
	}

	for name, lookup := range lookups {
		t.Run(name, func(t *testing.T) {
			// Identity, not errors.Is: a wrapped error would change what callers see.
			assert.True(t, lookup() == models.ErrRepositoryNotFound, "missing row must be the bare ErrRepositoryNotFound")
		})
	}
}

func TestAlreadyDelivered_MissingRow_IsNotDelivered(t *testing.T) {
	fixtures.TestDB(t)
	delivered, err := repositories.NewWebhookEventRepository(nil).AlreadyDelivered(context.Background(), uuid.NewString())
	require.NoError(t, err)
	assert.False(t, delivered)
}

func TestAlreadyDelivered_ReportsTheDeliveryState(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	events := repositories.NewWebhookEventRepository(nil)
	pending := &models.WebhookEvent{ID: uuid.New(), EventType: "deposit.confirmed", Payload: `{}`, DeliveryURL: "https://example.com/hook", DeliveryStatus: "pending", MaxAttempts: 10}
	require.NoError(t, events.Create(ctx, pending))

	delivered, err := events.AlreadyDelivered(ctx, pending.ID.String())
	require.NoError(t, err)
	assert.False(t, delivered)

	require.NoError(t, events.MarkDelivered(ctx, pending.ID.String()))
	delivered, err = events.AlreadyDelivered(ctx, pending.ID.String())
	require.NoError(t, err)
	assert.True(t, delivered)
}

func TestInvite_Lookups_FindOnlyOpenInvites(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	account := fixtures.InsertAccount(t, "invite-lookups")
	invites := repositories.NewAccountInviteRepository(nil)
	open := &models.AccountInvite{ID: uuid.New(), AccountID: account.ID, Email: "Open@Example.com", Role: models.AccountRoleUser, TokenHash: "hash-open", ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, invites.Create(ctx, open))

	byEmail, err := invites.FindPendingByAccountEmail(ctx, account.ID, "  open@example.com ")
	require.NoError(t, err)
	assert.Equal(t, open.ID, byEmail.ID)
	byAccount, err := invites.FindOpenByAccountAndID(ctx, account.ID, open.ID)
	require.NoError(t, err)
	assert.Equal(t, open.ID, byAccount.ID)
	byID, err := invites.FindOpenByID(ctx, open.ID)
	require.NoError(t, err)
	assert.Equal(t, open.ID, byID.ID)
	byToken, err := invites.FindPendingByTokenHash(ctx, "hash-open", time.Now())
	require.NoError(t, err)
	assert.Equal(t, open.ID, byToken.ID)

	_, err = invites.FindPendingByTokenHash(ctx, "hash-open", time.Now().Add(2*time.Hour))
	assert.True(t, err == models.ErrRepositoryNotFound, "an expired invite is not pending")

	exec(t, `UPDATE account_invites SET revoked_at = NOW() WHERE id = ?`, open.ID)
	_, err = invites.FindOpenByID(ctx, open.ID)
	assert.True(t, err == models.ErrRepositoryNotFound, "a revoked invite is not open")
	_, err = invites.FindPendingByAccountEmail(ctx, account.ID, "open@example.com")
	assert.True(t, err == models.ErrRepositoryNotFound)
}

func TestFindForOwnerAttach_PrefersTheLiveMembership(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	account := fixtures.InsertAccount(t, "owner-attach")
	user := fixtures.InsertUser(t)
	deleted, live := uuid.New(), uuid.New()
	exec(t, `INSERT INTO account_users (id, account_id, user_id, role, status, deleted_at, created_at, updated_at)
		VALUES (?, ?, ?, 'user', 'active', NOW(), NOW(), NOW())`, deleted, account.ID, user.ID)
	memberships := repositories.NewAccountUserRepository(nil)

	found, err := memberships.FindForOwnerAttach(ctx, account.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, deleted, found.ID, "with only a soft-deleted row, that row is returned")

	exec(t, `INSERT INTO account_users (id, account_id, user_id, role, status, created_at, updated_at)
		VALUES (?, ?, ?, 'admin', 'active', NOW(), NOW())`, live, account.ID, user.ID)
	found, err = memberships.FindForOwnerAttach(ctx, account.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, live, found.ID, "the live row wins over a soft-deleted one")
}

func TestFindByTransactionID_ReturnsTheLinkedWithdrawal(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	wallet := fixtures.InsertWallet(t, "eth")
	tx := fixtures.InsertTransaction(t, wallet.ID, nil, "eth", "withdrawal", "pending", "ETH", "0.001", 0)
	withdrawals := repositories.NewWithdrawalRepository(nil)
	w := &models.Withdrawal{ID: uuid.New(), WalletID: wallet.ID, TransactionID: &tx.ID, Status: "pending", Amount: "0.001", FeeEstimate: "0", DestinationAddress: "0xdest"}
	require.NoError(t, withdrawals.Create(ctx, w))

	found, err := withdrawals.FindByTransactionID(ctx, tx.ID)
	require.NoError(t, err)
	assert.Equal(t, w.ID, found.ID)
}
