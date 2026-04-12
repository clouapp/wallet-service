package rules

import (
	"context"
	"strconv"

	"github.com/goravel/framework/contracts/validation"
)

type IntegerString struct{}

func (r *IntegerString) Signature() string {
	return "integer_string"
}

func (r *IntegerString) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	if !ok || s == "" {
		return true
	}
	_, err := strconv.Atoi(s)
	return err == nil
}

func (r *IntegerString) Message(_ context.Context) string {
	return "The :attribute must be a valid integer."
}
