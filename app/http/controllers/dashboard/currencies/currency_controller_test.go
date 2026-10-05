package currencies

import (
	"testing"

	currencysvc "github.com/macrowallets/waas/app/services/currencies"
	"github.com/macrowallets/waas/app/services/price"
)

func currenciesControllerDeps() CurrenciesControllerDeps {
	return CurrenciesControllerDeps{
		Currencies: &currencysvc.Service{},
		Prices:     &price.Service{},
	}
}

func TestNewCurrenciesControllerKeepsItsDependencies(t *testing.T) {
	deps := currenciesControllerDeps()
	ctrl := NewCurrenciesController(deps)
	if ctrl == nil {
		t.Fatal("NewCurrenciesController returned nil")
	}
	if ctrl.currencies != deps.Currencies {
		t.Fatal("currencies controller did not keep the currencies service")
	}
	if ctrl.prices != deps.Prices {
		t.Fatal("currencies controller did not keep the price service")
	}
}

func TestNewCurrenciesControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*CurrenciesControllerDeps)
		panic string
	}{
		{
			name:  "currencies service",
			clear: func(deps *CurrenciesControllerDeps) { deps.Currencies = nil },
			panic: "dashboard currencies controller: currencies service is required",
		},
		{
			name:  "price service",
			clear: func(deps *CurrenciesControllerDeps) { deps.Prices = nil },
			panic: "dashboard currencies controller: price service is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := currenciesControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewCurrenciesController(deps)
			t.Fatal("expected a panic")
		})
	}
}
