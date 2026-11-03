package ramps

import (
	"context"
	"github.com/macrowallets/waas/app/models"
	em "github.com/macrowallets/waas/packages/rampengine/app/models"
	er "github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

type BusinessDeclaration struct {
	Profile em.BusinessProfile
	People  []em.BusinessPerson
}

func (s *Service) BusinessProfile(ctx context.Context, row *models.RampCustomer) (*BusinessDeclaration, error) {
	if row.EntityType != "business" {
		return nil, ErrCustomerNotFound
	}
	repo := er.NewCustomerBusinessProfileRepository()
	profiles, err := repo.FindByCustomers([]uint{row.EngineCustomerID})
	if err != nil {
		return nil, err
	}
	p, ok := profiles[row.EngineCustomerID]
	if !ok {
		return nil, nil
	}
	people, err := repo.ListPeople(row.EngineCustomerID)
	if err != nil {
		return nil, err
	}
	return &BusinessDeclaration{p, people}, nil
}
