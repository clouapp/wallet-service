package bank

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFingerprintIsKeyedStableAndSeparatesFields(t *testing.T) {
	a := Fingerprint("021000021", "123456789")
	assert.Len(t, a, 64)
	assert.Equal(t, a, Fingerprint("021000021", "123456789"))
	assert.NotEqual(t, a, Fingerprint("021000021", "123456780"))
	// The separator keeps "0210000211" + "23456789" from colliding.
	assert.NotEqual(t, a, Fingerprint("0210000211", "23456789"))
	assert.NotContains(t, a, "123456789")
}

func TestLast4AndMask(t *testing.T) {
	assert.Equal(t, "6789", Last4("123456789"))
	assert.Equal(t, "1234", Last4("1234"))
	assert.Equal(t, "••••6789", Mask("6789"))
}
