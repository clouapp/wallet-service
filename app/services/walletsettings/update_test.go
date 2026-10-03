package walletsettings

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
)

func intPointer(value int) *int { return &value }

func mustParse(t *testing.T, body string) Update {
	t.Helper()
	update, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse(%s): %v", body, err)
	}
	return update
}

func requireFieldError(t *testing.T, err error, field string) {
	t.Helper()
	var fieldErr *FieldError
	if !errors.As(err, &fieldErr) || fieldErr.Field != field {
		t.Fatalf("err %v, want a FieldError on %s", err, field)
	}
}

func TestParseAcceptsNumbersStringsAndNull(t *testing.T) {
	update := mustParse(t, `{"fee_multiplier": 1.25, "fee_rate_min": "2", "fee_rate_max": null, "label": "  Treasury  "}`)
	if !update.FeeMultiplier.Set || !update.FeeMultiplier.Value.Equal(decimal.RequireFromString("1.25")) {
		t.Fatalf("fee_multiplier %+v", update.FeeMultiplier)
	}
	if !update.FeeRateMin.Set || update.FeeRateMin.Value != 2 || !update.FeeRateMax.Set || !update.FeeRateMax.Null {
		t.Fatalf("fee rates %+v / %+v", update.FeeRateMin, update.FeeRateMax)
	}
	if update.Label.Value != "Treasury" || update.RequiredApprovals.Set {
		t.Fatalf("label %q required_approvals %+v", update.Label.Value, update.RequiredApprovals)
	}

	exact := mustParse(t, `{"fee_multiplier": "4.9999"}`)
	if exact.FeeMultiplier.Value.String() != "4.9999" {
		t.Fatalf("decimal string kept exactly, got %s", exact.FeeMultiplier.Value)
	}
	reset := mustParse(t, `{"fee_multiplier": null}`)
	if !reset.FeeMultiplier.Set || !reset.FeeMultiplier.Null {
		t.Fatalf("null resets, got %+v", reset.FeeMultiplier)
	}
}

func TestParseRejectsInvalidBodies(t *testing.T) {
	cases := []struct {
		name, body, field string
	}{
		{"not an object", `[1]`, "body"},
		{"malformed", `{"fee_multiplier":`, "body"},
		{"unknown field", `{"fee_multiplier": 1.2, "gas_price": 9}`, "gas_price"},
		{"freeze is its own endpoint", `{"frozen_until": "2030-01-01T00:00:00Z"}`, "frozen_until"},
		{"multiplier below 1", `{"fee_multiplier": 0.5}`, FieldFeeMultiplier},
		{"multiplier above 5", `{"fee_multiplier": 5.0001}`, FieldFeeMultiplier},
		{"multiplier with 5 decimals", `{"fee_multiplier": 1.00001}`, FieldFeeMultiplier},
		{"multiplier not a number", `{"fee_multiplier": "fast"}`, FieldFeeMultiplier},
		{"multiplier NaN", `{"fee_multiplier": "NaN"}`, FieldFeeMultiplier},
		{"multiplier boolean", `{"fee_multiplier": true}`, FieldFeeMultiplier},
		{"fractional fee rate", `{"fee_rate_min": 1.5}`, FieldFeeRateMin},
		{"required approvals null", `{"required_approvals": null}`, FieldRequiredApprovals},
		{"empty label", `{"label": "   "}`, FieldLabel},
		{"null label", `{"label": null}`, FieldLabel},
		{"label too long", `{"label": "` + strings.Repeat("a", LabelMaxLength+1) + `"}`, FieldLabel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.body))
			requireFieldError(t, err, tc.field)
		})
	}
	if _, err := Parse([]byte(`{}`)); !errors.Is(err, ErrNoFields) {
		t.Fatalf("empty object: %v", err)
	}
	if _, err := Parse([]byte(`{"label":"` + strings.Repeat("a", MaxBodyBytes) + `"}`)); err == nil {
		t.Fatal("an oversized body must be refused")
	}
}

func TestColumnsAppliesChainRulesAndBounds(t *testing.T) {
	btcWallet := &models.Wallet{Chain: models.ChainBTC, FeeRateMin: intPointer(3), FeeRateMax: intPointer(40)}
	evmWallet := &models.Wallet{Chain: models.ChainBase}

	columns, err := mustParse(t, `{"fee_multiplier": 1.5}`).Columns(evmWallet, models.AdapterTypeEVM)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := columns[FieldFeeMultiplier].(numeric.NullDecimal)
	if !ok || !stored.Valid || !stored.Decimal.Equal(decimal.RequireFromString("1.5")) {
		t.Fatalf("columns %+v", columns)
	}

	columns, err = mustParse(t, `{"fee_multiplier": null}`).Columns(evmWallet, models.AdapterTypeSolana)
	if err != nil {
		t.Fatalf("resetting is allowed on any chain: %v", err)
	}
	if reset, ok := columns[FieldFeeMultiplier].(numeric.NullDecimal); !ok || reset.Valid {
		t.Fatalf("columns %+v", columns)
	}

	_, err = mustParse(t, `{"fee_multiplier": 2}`).Columns(evmWallet, models.AdapterTypeSolana)
	requireFieldError(t, err, FieldFeeMultiplier)

	_, err = mustParse(t, `{"fee_rate_min": 2}`).Columns(evmWallet, models.AdapterTypeEVM)
	requireFieldError(t, err, FieldFeeRateMin)

	_, err = mustParse(t, `{"fee_rate_min": 50}`).Columns(btcWallet, models.AdapterTypeBitcoin)
	requireFieldError(t, err, FieldFeeRateMin) // 50 > the stored max 40

	_, err = mustParse(t, `{"fee_rate_max": 10001}`).Columns(btcWallet, models.AdapterTypeBitcoin)
	requireFieldError(t, err, FieldFeeRateMax)

	columns, err = mustParse(t, `{"fee_rate_min": null, "fee_rate_max": 60}`).Columns(btcWallet, models.AdapterTypeBitcoin)
	if err != nil || columns[FieldFeeRateMin] != nil || columns[FieldFeeRateMax] != 60 {
		t.Fatalf("columns %+v err %v", columns, err)
	}
	if _, present := columns[FieldFeeRateMin]; !present {
		t.Fatal("a null bound must be written as NULL")
	}

	_, err = mustParse(t, `{"required_approvals": 11}`).Columns(evmWallet, models.AdapterTypeEVM)
	requireFieldError(t, err, FieldRequiredApprovals)

	columns, err = mustParse(t, `{"label": "Ops"}`).Columns(evmWallet, models.AdapterTypeEVM)
	if err != nil || columns[FieldLabel] != "Ops" || len(columns) != 1 {
		t.Fatalf("columns %+v err %v", columns, err)
	}
	if _, err := mustParse(t, `{"label": "Ops"}`).Columns(nil, models.AdapterTypeEVM); err == nil {
		t.Fatal("a nil wallet must be refused")
	}
}
