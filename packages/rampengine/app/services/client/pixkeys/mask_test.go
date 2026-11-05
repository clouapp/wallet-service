package pixkeys

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMask(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		keyType string
		want    string
	}{
		{name: "cpf", key: "39053344705", keyType: "cpf", want: "***.533.447-**"},
		{name: "cnpj", key: "12345678000195", keyType: "cnpj", want: "12.***.***/****-95"},
		{name: "email", key: "maria.silva@example.com", keyType: "email", want: "m*********a@example.com"},
		{name: "phone", key: "+5511999887766", keyType: "phone", want: "****7766"},
		{name: "unknown cpf", key: "39053344705", keyType: "", want: "***********"},
		{name: "invalid cpf length", key: "12345", keyType: "cpf", want: "*****"},
		{name: "empty key", key: "  ", keyType: "cpf", want: ""},
		{name: "email with a two-letter local part", key: "ab@example.com", keyType: "email", want: "**@example.com"},
		{name: "email with a one-letter local part", key: "a@example.com", keyType: "email", want: "*************"},
		// Fullwidth digits are three bytes each: slicing the last four bytes
		// of them rendered a broken fragment of a character instead of a mask.
		{name: "phone in fullwidth digits", key: "１１９９８８７７６６", keyType: "phone", want: strings.Repeat("*", 30)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, Mask(tt.key, tt.keyType))
		})
	}
}

func TestMask_UnknownTypeFullyRedactsCPF(t *testing.T) {
	got := Mask("39053344705", "")
	require.Equal(t, "***********", got)
	require.NotContains(t, got, "390")
}

func TestMask_CNPJShowsBaseNotBranch(t *testing.T) {
	got := Mask("12345678000195", "cnpj")
	require.Equal(t, "12.***.***/****-95", got)
}

// An alphanumeric CNPJ is masked as a numeric one is, on its canonical
// spelling, however it was stored.
func TestMask_AlphanumericCNPJ(t *testing.T) {
	require.Equal(t, "12.***.***/****-35", Mask("12ABC34501DE35", "cnpj"))
	require.Equal(t, "12.***.***/****-35", Mask("12.abc.345/01de-35", "cnpj"))
	require.Equal(t, "12.***.***/****-95", Mask("12.345.678/0001-95", "cnpj"))
}
