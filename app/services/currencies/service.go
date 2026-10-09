package currencies

import (
	"context"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/app/models"
)

// Store is the currency catalogue this service reads.
type Store interface {
	FindAllActive(ctx context.Context) ([]models.Currency, error)
	FindByCode(ctx context.Context, code string) (*models.Currency, error)
}

// Service reads the currency catalogue for dashboard handlers.
type Service struct {
	store Store
}

// Deps is everything the currency catalogue service reads. A nil Store is
// reported when a method runs, as the missing-repository error.
type Deps struct {
	Store Store
}

// NewService builds a currency catalogue service.
func NewService(deps Deps) *Service {
	return &Service{store: deps.Store}
}

// FindAllActive returns every active currency, in the store's order.
func (s *Service) FindAllActive(ctx context.Context) ([]models.Currency, error) {
	if ctx == nil {
		return nil, fmt.Errorf("list currencies: context is required")
	}
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("currencies service: currencies repository is required")
	}
	return s.store.FindAllActive(ctx)
}

// FindByCode returns the currency for code. A missing row is the store's
// not-found error, which the handler already maps to 404.
func (s *Service) FindByCode(ctx context.Context, code string) (*models.Currency, error) {
	if ctx == nil {
		return nil, fmt.Errorf("find currency: context is required")
	}
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("currencies service: currencies repository is required")
	}
	return s.store.FindByCode(ctx, code)
}

// ErrNotFound is Get's answer for a currency code the catalogue does not hold.
var ErrNotFound = errors.New("currency not found")

// Get returns the currency for code. A missing row, and a store that answers
// nothing, are ErrNotFound; any other store failure passes through.
func (s *Service) Get(ctx context.Context, code string) (*models.Currency, error) {
	currency, err := s.FindByCode(ctx, code)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return nil, err
	}
	if err != nil || currency == nil {
		return nil, ErrNotFound
	}
	return currency, nil
}
