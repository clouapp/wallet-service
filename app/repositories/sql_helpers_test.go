package repositories_test

import (
	"testing"

	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"
)

func exec(t *testing.T, statement string, args ...any) {
	t.Helper()
	_, err := facades.Orm().Query().Exec(statement, args...)
	require.NoError(t, err, statement)
}

func scalar[T any](t *testing.T, query string, args ...any) T {
	t.Helper()
	var value T
	require.NoError(t, facades.Orm().Query().Raw(query, args...).Scan(&value), query)
	return value
}
