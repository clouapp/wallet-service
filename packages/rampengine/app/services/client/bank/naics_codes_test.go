package bank

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNaicsCodesMatchTheBlindPayFixture(t *testing.T) {
	f, err := os.Open("../../../adapters/ramp/blindpay/testdata/openapi-2026-10-15.json.gz")
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Enum []any `json:"enum"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	require.NoError(t, json.NewDecoder(gz).Decode(&doc))
	want := doc.Components.Schemas["CreateBankAccountIn"].Properties["business_industry"].Enum
	require.NotEmpty(t, want)
	require.Len(t, naicsCodes, len(want))
	for _, v := range want {
		code, isString := v.(string)
		require.True(t, isString, "NAICS enum entry %v is not a string", v)
		_, ok := naicsCodes[code]
		require.True(t, ok, "NAICS %s from the fixture is missing", code)
	}
}
