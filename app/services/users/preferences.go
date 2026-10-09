package users

import (
	"context"
	"errors"

	"github.com/macrowallets/waas/app/models"
)

// ErrUnknownFiat is a preferred fiat code that is not an active fiat currency.
var ErrUnknownFiat = errors.New("invalid fiat currency code")

// CurrencyFinder looks a currency up by its code (currencies.Service).
type CurrencyFinder interface {
	FindByCode(ctx context.Context, code string) (*models.Currency, error)
}

// PreferencesInput is PUT /v1/me/preferences. A blank PreferredFiatCode and a
// nil DisplayInFiat are left as stored.
type PreferencesInput struct {
	PreferredFiatCode string
	DisplayInFiat     *bool
}

// ChangePreferences applies the change to the preferences of the user the
// caller already loaded, stores the whole document and returns it. A fiat code
// that is not an active fiat currency is ErrUnknownFiat and nothing is stored.
func (s *Service) ChangePreferences(ctx context.Context, user *models.User, in PreferencesInput) (*models.UserPreferences, error) {
	prefs := user.Preferences
	if prefs == nil {
		prefs = &models.UserPreferences{}
	}
	if in.PreferredFiatCode != "" {
		if !s.activeFiat(ctx, in.PreferredFiatCode) {
			return nil, ErrUnknownFiat
		}
		prefs.PreferredFiatCode = in.PreferredFiatCode
	}
	if in.DisplayInFiat != nil {
		prefs.DisplayInFiat = in.DisplayInFiat
	}
	if err := s.UpdatePreferences(ctx, user.ID, prefs); err != nil {
		return nil, err
	}
	return prefs, nil
}

// activeFiat reports whether code is an active fiat currency. A lookup that
// fails is not one.
func (s *Service) activeFiat(ctx context.Context, code string) bool {
	if s.currencies == nil {
		return false
	}
	currency, err := s.currencies.FindByCode(ctx, code)
	return err == nil && currency != nil && currency.Active && currency.Type == models.CurrencyTypeFiat
}
