package users_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/users"
)

type knownCurrencies map[string]*models.Currency

func (c knownCurrencies) FindByCode(_ context.Context, code string) (*models.Currency, error) {
	currency, ok := c[code]
	if !ok {
		return nil, models.ErrRepositoryNotFound
	}
	return currency, nil
}

func preferencesService(store *fakeStore) *users.Service {
	return users.NewService(users.Deps{
		Store: store,
		Currencies: knownCurrencies{
			"EUR": {Code: "EUR", Type: models.CurrencyTypeFiat, Active: true},
			"BRL": {Code: "BRL", Type: models.CurrencyTypeFiat, Active: false},
			"BTC": {Code: "BTC", Type: "crypto", Active: true},
		},
	})
}

func TestChange_Preferences_StoresTheChangeOnTheUsersPreferences(t *testing.T) {
	store := &fakeStore{}
	off := false
	user := &models.User{ID: uuid.New()}

	prefs, err := preferencesService(store).ChangePreferences(context.Background(), user, users.PreferencesInput{PreferredFiatCode: "EUR", DisplayInFiat: &off})

	require.NoError(t, err)
	assert.Equal(t, user.ID, store.id)
	assert.Same(t, prefs, store.prefs)
	assert.Equal(t, "EUR", prefs.PreferredFiatCode)
	assert.False(t, prefs.IsDisplayInFiat())
}

func TestChange_Preferences_LeavesWhatIsNotSentAsStored(t *testing.T) {
	store := &fakeStore{}
	on := true
	user := &models.User{ID: uuid.New(), Preferences: &models.UserPreferences{PreferredFiatCode: "EUR", DisplayInFiat: &on}}

	prefs, err := preferencesService(store).ChangePreferences(context.Background(), user, users.PreferencesInput{})

	require.NoError(t, err)
	assert.Equal(t, "EUR", prefs.PreferredFiatCode)
	assert.True(t, prefs.IsDisplayInFiat())
}

func TestChange_Preferences_RefusesACodeThatIsNotAnActiveFiat(t *testing.T) {
	for _, code := range []string{"XXX", "BRL", "BTC"} {
		t.Run(code, func(t *testing.T) {
			store := &fakeStore{}
			_, err := preferencesService(store).ChangePreferences(context.Background(), &models.User{ID: uuid.New()}, users.PreferencesInput{PreferredFiatCode: code})
			assert.ErrorIs(t, err, users.ErrUnknownFiat)
			assert.Nil(t, store.prefs, "a refused change is not stored")
		})
	}
}

func TestChange_Preferences_ReturnsTheFailedWrite(t *testing.T) {
	store := &fakeStore{err: errors.New("store down")}

	_, err := preferencesService(store).ChangePreferences(context.Background(), &models.User{ID: uuid.New()}, users.PreferencesInput{})

	assert.ErrorIs(t, err, store.err)
}
