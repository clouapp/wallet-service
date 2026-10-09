package currencies

import (
	"testing"

	currencysvc "github.com/macrowallets/waas/app/services/currencies"
	"github.com/macrowallets/waas/app/services/price"
)

func TestNew_CurrencyController_KeepsItsDependencies(t *testing.T) {
	currencies, prices := &currencysvc.Service{}, &price.Service{}

	ctrl := NewCurrencyController(currencies, prices)

	if ctrl.currencies != currencies {
		t.Fatal("currency controller did not keep the currencies service")
	}
	if ctrl.prices != prices {
		t.Fatal("currency controller did not keep the price service")
	}
}

func TestNew_CurrencyController_RequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name       string
		currencies *currencysvc.Service
		prices     *price.Service
		panic      string
	}{
		{"currencies service", nil, &price.Service{}, "dashboard currencies controller: currencies service is required"},
		{"price service", &currencysvc.Service{}, nil, "dashboard currencies controller: price service is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewCurrencyController(tc.currencies, tc.prices)
			t.Fatal("expected a panic")
		})
	}
}
