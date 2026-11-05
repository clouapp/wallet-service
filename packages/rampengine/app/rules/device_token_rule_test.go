package rules

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeviceTokenRule_Passes(t *testing.T) {
	r := &DeviceTokenRule{}
	ctx := context.Background()

	for _, token := range []string{
		"a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90",
		"fXk3:APA91bHq-example_registration_token",
	} {
		assert.True(t, r.Passes(ctx, mapData{}, token), token)
	}

	// Each would be refused by the vendor anyway, and the first two would
	// change the meaning of the path the unregister reads the token from.
	for _, token := range []any{"abc/../def", "abc?x=1", "with space", "", strings.Repeat("a", 1025), 42} {
		assert.False(t, r.Passes(ctx, mapData{}, token), "%v", token)
	}
}
