package currencies_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/currencies"
)

func TestFindAllActiveAndByCode(t *testing.T) {
	t.Parallel()

	usd := &models.Currency{Code: "USD", Active: true}
	store := &fakeStore{active: []models.Currency{*usd}, byCode: map[string]*models.Currency{"USD": usd}}
	svc := currencies.NewService(store)

	got, err := svc.FindAllActive(context.Background())
	require.NoError(t, err)
	require.Equal(t, []models.Currency{*usd}, got)
	require.Equal(t, 1, store.activeCalls)

	found, err := svc.FindByCode(context.Background(), "USD")
	require.NoError(t, err)
	require.Equal(t, usd, found)
	require.Equal(t, "USD", store.lastCode)

	store.err = errors.New("store down")
	_, err = svc.FindByCode(context.Background(), "EUR")
	require.ErrorIs(t, err, store.err)
}

func TestCurrencyReadsRequireContextAndStore(t *testing.T) {
	t.Parallel()

	_, err := currencies.NewService(&fakeStore{}).FindAllActive(nil)
	require.EqualError(t, err, "list currencies: context is required")

	_, err = currencies.NewService(nil).FindByCode(context.Background(), "USD")
	require.EqualError(t, err, "currencies service: currencies repository is required")

	var svc *currencies.Service
	_, err = svc.FindAllActive(context.Background())
	require.EqualError(t, err, "currencies service: currencies repository is required")
}

type fakeStore struct {
	active      []models.Currency
	byCode      map[string]*models.Currency
	err         error
	activeCalls int
	lastCode    string
}

func (f *fakeStore) FindAllActive(context.Context) ([]models.Currency, error) {
	f.activeCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.active, nil
}

func (f *fakeStore) FindByCode(_ context.Context, code string) (*models.Currency, error) {
	f.lastCode = code
	if f.err != nil {
		return nil, f.err
	}
	return f.byCode[code], nil
}
