package config

import (
	"reflect"
	"testing"
)

func TestCors_Origins_ParseTheEnvList(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"unset falls back to the local front ends", "", []string{"http://localhost:3000", "http://localhost:3001"}},
		{"blank falls back too", " , ", []string{"http://localhost:3000", "http://localhost:3001"}},
		{"one origin", "https://app.example", []string{"https://app.example"}},
		{"trimmed, empties dropped", " https://a.example , ,https://b.example", []string{"https://a.example", "https://b.example"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := corsOrigins(tc.raw); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("corsOrigins(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
