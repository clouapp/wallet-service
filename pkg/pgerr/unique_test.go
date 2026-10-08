package pgerr

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsUniqueViolation(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"nil":               {nil, false},
		"duplicate key":     {errors.New(`ERROR: duplicate key value violates unique constraint "x" (SQLSTATE 23505)`), true},
		"sqlstate only":     {errors.New("insert failed (SQLSTATE 23505)"), true},
		"upper case":        {errors.New("DUPLICATE KEY value"), true},
		"wrapped":           {fmt.Errorf("create: %w", errors.New("duplicate key")), true},
		"other constraint":  {errors.New("violates foreign key constraint (SQLSTATE 23503)"), false},
		"unrelated failure": {errors.New("connection refused"), false},
	}
	for name, tc := range cases {
		if got := IsUniqueViolation(tc.err); got != tc.want {
			t.Errorf("%s: IsUniqueViolation = %v, want %v", name, got, tc.want)
		}
	}
}
