package rules

import "testing"

// The Receita issues CNPJs with letters in the first twelve positions since
// 31/07/2026, and every numeric one stays valid. 12ABC34501DE35 is the Serpro
// example: Bridge's sandbox and BrasilAPI both take its check digits and both
// refuse ...36 (PLANO, 18/10/2026). 12ABN34501DE35 shares every digit with it,
// so only the letters tell the two companies apart.
func TestIsValidCNPJ_Alphanumeric(t *testing.T) {
	cases := map[string]bool{
		"12ABC34501DE35": true,
		"12ABN34501DE35": true,
		"12XYZ34501DE02": true,
		"AB123456000110": true,
		"12ABC34501DE36": false,
		"12ABC34501DE53": false,
		// The canonical spelling is upper case: lower case is a spelling the
		// normalization fixes, not a CNPJ.
		"12abc34501de35": false,
		// The two check digits are always digits.
		"12ABC34501DE3A": false,
		"12ABC34501D-35": false,
		"AAAAAAAAAAAAAA": false,
		// Every numeric CNPJ the suite used before keeps its answer.
		"11222333000181": true,
		"12345678000195": true,
		"33000167000101": true,
		"11222333000182": false,
		"11111111111111": false,
	}
	for cnpj, want := range cases {
		if got := isValidCNPJ(cnpj); got != want {
			t.Errorf("isValidCNPJ(%q) = %v, want %v", cnpj, got, want)
		}
	}
}
