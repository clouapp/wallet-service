package bridge

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBankPaymentRail(t *testing.T) {
	for raw, want := range map[string]string{
		"ach_push":     "ach",
		"ach":          "ach",
		"ach_same_day": "ach",
		"wire":         "wire",
		"fednow":       "fednow",
		"pix":          "pix",
		"sepa":         "",
		"":             "",
	} {
		require.Equal(t, want, bankPaymentRail(raw), raw)
	}
}
