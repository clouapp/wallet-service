package walletrecords_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

type memoryWhitelist struct {
	walletrecords.WhitelistStore
	created   *models.WhitelistEntry
	createErr error
	found     *models.WhitelistEntry
	findErr   error
	deleted   *models.WhitelistEntry
	deleteErr error
}

func (m *memoryWhitelist) Create(_ context.Context, entry *models.WhitelistEntry) error {
	m.created = entry
	return m.createErr
}

func (m *memoryWhitelist) FindByIDAndWallet(context.Context, uuid.UUID, uuid.UUID) (*models.WhitelistEntry, error) {
	return m.found, m.findErr
}

func (m *memoryWhitelist) Delete(_ context.Context, entry *models.WhitelistEntry) error {
	m.deleted = entry
	return m.deleteErr
}

func TestWhitelist_Add(t *testing.T) {
	wallet := uuid.New()

	t.Run("stores a new entry of the wallet", func(t *testing.T) {
		store := &memoryWhitelist{}

		entry, err := walletrecords.NewWhitelist(store).Add(context.Background(), wallet, "bc1qabc", "Cold Storage")

		require.NoError(t, err)
		assert.Same(t, store.created, entry)
		assert.NotEqual(t, uuid.Nil, entry.ID)
		assert.Equal(t, wallet, entry.WalletID)
		assert.Equal(t, "bc1qabc", entry.Address)
		assert.Equal(t, "Cold Storage", entry.Label)
	})

	t.Run("returns nothing when the store fails", func(t *testing.T) {
		boom := errors.New("pq: down")

		entry, err := walletrecords.NewWhitelist(&memoryWhitelist{createErr: boom}).Add(context.Background(), wallet, "bc1qabc", "")

		assert.ErrorIs(t, err, boom)
		assert.Nil(t, entry)
	})
}

func TestWhitelist_Remove(t *testing.T) {
	entry := &models.WhitelistEntry{ID: uuid.New()}

	t.Run("deletes the entry the wallet holds", func(t *testing.T) {
		store := &memoryWhitelist{found: entry}

		require.NoError(t, walletrecords.NewWhitelist(store).Remove(context.Background(), uuid.New(), entry.ID))
		assert.Same(t, entry, store.deleted)
	})

	t.Run("an entry the wallet does not hold, or a failed lookup, is not found", func(t *testing.T) {
		for name, store := range map[string]*memoryWhitelist{
			"no row":  {},
			"missing": {findErr: models.ErrRepositoryNotFound},
			"outage":  {findErr: errors.New("pq: down")},
		} {
			err := walletrecords.NewWhitelist(store).Remove(context.Background(), uuid.New(), uuid.New())

			assert.ErrorIs(t, err, walletrecords.ErrWhitelistEntryNotFound, name)
			assert.Nil(t, store.deleted, name)
		}
	})

	t.Run("a failed delete is an error", func(t *testing.T) {
		boom := errors.New("pq: down")

		err := walletrecords.NewWhitelist(&memoryWhitelist{found: entry, deleteErr: boom}).Remove(context.Background(), uuid.New(), entry.ID)

		assert.ErrorIs(t, err, boom)
		assert.NotErrorIs(t, err, walletrecords.ErrWhitelistEntryNotFound)
	})
}

type memoryWebhooks struct {
	walletrecords.WebhookStore
	created   *models.WebhookConfig
	createErr error
	found     *models.WebhookConfig
	findErr   error
	deleted   *models.WebhookConfig
	deleteErr error
}

func (m *memoryWebhooks) Create(_ context.Context, cfg *models.WebhookConfig) error {
	m.created = cfg
	return m.createErr
}

func (m *memoryWebhooks) FindByIDAndWallet(context.Context, uuid.UUID, uuid.UUID) (*models.WebhookConfig, error) {
	return m.found, m.findErr
}

func (m *memoryWebhooks) Delete(_ context.Context, cfg *models.WebhookConfig) error {
	m.deleted = cfg
	return m.deleteErr
}

func TestWebhooks_Register(t *testing.T) {
	wallet := uuid.New()
	store := &memoryWebhooks{}

	cfg, err := walletrecords.NewWebhooks(store).Register(context.Background(), wallet, "https://example.com/hook", "wh_secret", "deposit.confirmed")

	require.NoError(t, err)
	assert.Same(t, store.created, cfg)
	assert.NotEqual(t, uuid.Nil, cfg.ID)
	require.NotNil(t, cfg.WalletID)
	assert.Equal(t, wallet, *cfg.WalletID)
	assert.Equal(t, "wallet", cfg.Type)
	assert.Equal(t, "https://example.com/hook", cfg.URL)
	assert.Equal(t, "wh_secret", cfg.Secret)
	assert.Equal(t, "deposit.confirmed", cfg.Events)

	boom := errors.New("pq: down")
	cfg, err = walletrecords.NewWebhooks(&memoryWebhooks{createErr: boom}).Register(context.Background(), wallet, "https://example.com/hook", "", "")
	assert.ErrorIs(t, err, boom)
	assert.Nil(t, cfg)
}

func TestWebhooks_Remove(t *testing.T) {
	cfg := &models.WebhookConfig{ID: uuid.New()}

	store := &memoryWebhooks{found: cfg}
	require.NoError(t, walletrecords.NewWebhooks(store).Remove(context.Background(), uuid.New(), cfg.ID))
	assert.Same(t, cfg, store.deleted)

	for name, missing := range map[string]*memoryWebhooks{"no row": {}, "outage": {findErr: errors.New("pq: down")}} {
		err := walletrecords.NewWebhooks(missing).Remove(context.Background(), uuid.New(), uuid.New())
		assert.ErrorIs(t, err, walletrecords.ErrWebhookNotFound, name)
		assert.Nil(t, missing.deleted, name)
	}
}

type recordingSender struct {
	cfg      *models.WebhookConfig
	walletID uuid.UUID
	err      error
}

func (r *recordingSender) SendTest(_ context.Context, cfg *models.WebhookConfig, walletID uuid.UUID) error {
	r.cfg, r.walletID = cfg, walletID
	return r.err
}

func TestWebhooks_SendTest(t *testing.T) {
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://example.com/hook"}
	wallet := uuid.New()

	t.Run("posts the test body of the webhook the wallet holds", func(t *testing.T) {
		sender := &recordingSender{}

		err := walletrecords.NewWebhooks(&memoryWebhooks{found: cfg}).SendTest(context.Background(), sender, wallet, cfg.ID)

		require.NoError(t, err)
		assert.Same(t, cfg, sender.cfg)
		assert.Equal(t, wallet, sender.walletID)
	})

	t.Run("a webhook the wallet does not hold sends nothing", func(t *testing.T) {
		sender := &recordingSender{}

		err := walletrecords.NewWebhooks(&memoryWebhooks{}).SendTest(context.Background(), sender, wallet, cfg.ID)

		assert.ErrorIs(t, err, walletrecords.ErrWebhookNotFound)
		assert.Nil(t, sender.cfg)
	})

	t.Run("a refused delivery is marked and keeps its cause", func(t *testing.T) {
		boom := errors.New("HTTP 500")

		err := walletrecords.NewWebhooks(&memoryWebhooks{found: cfg}).SendTest(context.Background(), &recordingSender{err: boom}, wallet, cfg.ID)

		assert.ErrorIs(t, err, walletrecords.ErrWebhookTestFailed)
		assert.ErrorIs(t, err, boom)
	})
}
