package seeders

import (
	"context"

	"github.com/macrowallets/waas/database/seeds"
)

type CurrencySeeder struct{}

func (s *CurrencySeeder) Signature() string {
	return "CurrencySeeder"
}

func (s *CurrencySeeder) Run() error {
	return seeds.SeedCurrencies(context.Background())
}
