package rules

import "testing"

func TestIsValidCPF(t *testing.T) {
	for _, tc := range []struct {
		cpf  string
		want bool
	}{
		{"390.533.447-05", true},
		{"39053344705", true},
		{"111.444.777-35", true},
		{"", false},
		{"123", false},
		{"00000000000", false},
		{"11111111111", false},
		{"39053344700", false},
		{"abcdefghijk", false},
		{"1234567890", false},
	} {
		if got := isValidCPF(tc.cpf); got != tc.want {
			t.Errorf("isValidCPF(%q) = %v, want %v", tc.cpf, got, tc.want)
		}
	}
}
