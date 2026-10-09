package walletsettings_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/walletsettings"
)

type fakeWallets struct {
	columns      map[string]any
	updateErr    error
	reloaded     *models.Wallet
	reloadErr    error
	statuses     []string
	statusErr    error
	frozenUntil  time.Time
	frozenUntilE error
}

func (f *fakeWallets) FindByID(context.Context, uuid.UUID) (*models.Wallet, error) {
	return f.reloaded, f.reloadErr
}

func (f *fakeWallets) UpdateSettings(_ context.Context, _ uuid.UUID, columns map[string]any) error {
	f.columns = columns
	return f.updateErr
}

func (f *fakeWallets) SetStatus(_ context.Context, _ uuid.UUID, status string) error {
	f.statuses = append(f.statuses, status)
	return f.statusErr
}

func (f *fakeWallets) SetFrozenUntil(_ context.Context, _ uuid.UUID, until time.Time) error {
	f.frozenUntil = until
	return f.frozenUntilE
}

type fakeChains struct {
	chain *models.Chain
	err   error
	reads int
}

func (f *fakeChains) FindByID(context.Context, string) (*models.Chain, error) {
	f.reads++
	return f.chain, f.err
}

type fakeNetworks struct{ asked string }

func (f *fakeNetworks) Network(_ context.Context, chainID string) models.ResolvedNetwork {
	f.asked = chainID
	return models.ResolvedNetwork{Name: "polygon-amoy", Testnet: true}
}

func newService(wallets *fakeWallets, chains *fakeChains, networks *fakeNetworks, now time.Time) *walletsettings.Service {
	return walletsettings.NewService(walletsettings.Deps{
		Wallets: wallets, Chains: chains, Networks: networks, Now: func() time.Time { return now },
	})
}

func TestNew_Service_RequiresEveryDependency(t *testing.T) {
	complete := walletsettings.Deps{Wallets: &fakeWallets{}, Chains: &fakeChains{}, Networks: &fakeNetworks{}}
	cases := []struct {
		name  string
		clear func(*walletsettings.Deps)
		panic string
	}{
		{"wallets", func(d *walletsettings.Deps) { d.Wallets = nil }, "wallet settings: wallets service is required"},
		{"chains", func(d *walletsettings.Deps) { d.Chains = nil }, "wallet settings: chains service is required"},
		{"networks", func(d *walletsettings.Deps) { d.Networks = nil }, "wallet settings: wallet network reads are required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := complete
			tc.clear(&deps)
			defer func() { assert.Equal(t, tc.panic, recover()) }()
			walletsettings.NewService(deps)
			t.Fatal("expected a panic")
		})
	}
}

func TestUpdate(t *testing.T) {
	wallet := &models.Wallet{ID: uuid.New(), Chain: "eth", Label: "hot"}
	reloaded := &models.Wallet{ID: wallet.ID, Chain: "eth", Label: "cold"}
	update := func(svc *walletsettings.Service, body string) (*models.Wallet, error) {
		return svc.Update(context.Background(), walletsettings.UpdateInput{Wallet: wallet, Body: []byte(body), ActorID: uuid.New()})
	}

	t.Run("a label change stores the column and returns the reloaded wallet without reading the chain", func(t *testing.T) {
		wallets, chains := &fakeWallets{reloaded: reloaded}, &fakeChains{}
		svc := newService(wallets, chains, &fakeNetworks{}, time.Now())

		got, err := update(svc, `{"label":"cold"}`)

		require.NoError(t, err)
		assert.Same(t, reloaded, got)
		assert.Equal(t, map[string]any{"label": "cold"}, wallets.columns)
		assert.Zero(t, chains.reads)
	})

	t.Run("a fee change reads the chain for its adapter type", func(t *testing.T) {
		wallets := &fakeWallets{reloaded: reloaded}
		chains := &fakeChains{chain: &models.Chain{ID: "eth", AdapterType: models.AdapterTypeEVM}}
		svc := newService(wallets, chains, &fakeNetworks{}, time.Now())

		_, err := update(svc, `{"fee_multiplier":1.5}`)

		require.NoError(t, err)
		assert.Equal(t, 1, chains.reads)
		assert.Contains(t, wallets.columns, "fee_multiplier")
	})

	t.Run("a fee change on a chain that cannot be read is refused before anything is stored", func(t *testing.T) {
		for name, chains := range map[string]*fakeChains{"no row": {}, "a failure": {err: errors.New("pq: down")}} {
			wallets := &fakeWallets{reloaded: reloaded}

			_, err := update(newService(wallets, chains, &fakeNetworks{}, time.Now()), `{"fee_rate_min":2}`)

			assert.ErrorIs(t, err, walletsettings.ErrChainNotFound, name)
			assert.Nil(t, wallets.columns, name)
		}
	})

	t.Run("the body's own refusals pass through and store nothing", func(t *testing.T) {
		wallets := &fakeWallets{reloaded: reloaded}
		svc := newService(wallets, &fakeChains{}, &fakeNetworks{}, time.Now())

		_, noFields := update(svc, `{}`)
		_, unknown := update(svc, `{"nope":1}`)

		assert.ErrorIs(t, noFields, walletsettings.ErrNoFields)
		var field *walletsettings.FieldError
		assert.ErrorAs(t, unknown, &field)
		assert.Nil(t, wallets.columns)
	})

	t.Run("a store failure on write or on reload is an error", func(t *testing.T) {
		boom := errors.New("pq: down")
		cases := map[string]*fakeWallets{
			"write":          {updateErr: boom, reloaded: reloaded},
			"reload":         {reloadErr: boom},
			"reload nothing": {},
		}
		for name, wallets := range cases {
			_, err := update(newService(wallets, &fakeChains{}, &fakeNetworks{}, time.Now()), `{"label":"cold"}`)
			assert.Error(t, err, name)
		}
	})
}

func TestFreeze(t *testing.T) {
	now := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	id := uuid.New()

	t.Run("without an end it lasts the default and sets the frozen status", func(t *testing.T) {
		wallets := &fakeWallets{}

		until, err := newService(wallets, &fakeChains{}, &fakeNetworks{}, now).Freeze(context.Background(), id, nil)

		require.NoError(t, err)
		assert.Equal(t, now.Add(walletsettings.DefaultFreeze), until)
		assert.Equal(t, until, wallets.frozenUntil)
		assert.Equal(t, []string{models.WalletStatusFrozen}, wallets.statuses)
	})

	t.Run("an end the caller names is kept", func(t *testing.T) {
		wallets := &fakeWallets{}
		end := now.Add(72 * time.Hour)

		until, err := newService(wallets, &fakeChains{}, &fakeNetworks{}, now).Freeze(context.Background(), id, &end)

		require.NoError(t, err)
		assert.Equal(t, end, until)
	})

	t.Run("a failed write is an error and the status is left alone when the end was not stored", func(t *testing.T) {
		wallets := &fakeWallets{frozenUntilE: errors.New("pq: down")}

		_, err := newService(wallets, &fakeChains{}, &fakeNetworks{}, now).Freeze(context.Background(), id, nil)

		require.Error(t, err)
		assert.Empty(t, wallets.statuses)

		_, err = newService(&fakeWallets{statusErr: errors.New("pq: down")}, &fakeChains{}, &fakeNetworks{}, now).Freeze(context.Background(), id, nil)
		require.Error(t, err)
	})
}

func TestArchive(t *testing.T) {
	wallet := &models.Wallet{ID: uuid.New(), Chain: "polygon", Status: models.StatusActive}

	t.Run("archives the wallet and returns it archived with its network, leaving the input alone", func(t *testing.T) {
		wallets, networks := &fakeWallets{}, &fakeNetworks{}

		archived, err := newService(wallets, &fakeChains{}, networks, time.Now()).Archive(context.Background(), wallet)

		require.NoError(t, err)
		assert.Equal(t, []string{models.WalletStatusArchived}, wallets.statuses)
		assert.Equal(t, models.WalletStatusArchived, archived.Wallet.Status)
		assert.Equal(t, models.StatusActive, wallet.Status)
		assert.Equal(t, "polygon", networks.asked)
		assert.True(t, archived.Network.Testnet)
	})

	t.Run("an archived wallet is refused", func(t *testing.T) {
		wallets := &fakeWallets{}

		_, err := newService(wallets, &fakeChains{}, &fakeNetworks{}, time.Now()).Archive(context.Background(), &models.Wallet{Status: models.WalletStatusArchived})

		assert.ErrorIs(t, err, walletsettings.ErrAlreadyArchived)
		assert.Empty(t, wallets.statuses)
	})

	t.Run("a failed write is an error", func(t *testing.T) {
		_, err := newService(&fakeWallets{statusErr: errors.New("pq: down")}, &fakeChains{}, &fakeNetworks{}, time.Now()).Archive(context.Background(), wallet)
		require.Error(t, err)
	})
}
