package ramp_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

func TestPixAccountRegistrationNeverPrintsItsData(t *testing.T) {
	reg := ramp.PixAccountRegistration{
		Name: "xip-pix-abc", PixKeyType: "email", PixKey: "ana@example.com",
		RecipientName: "Ana Souza", RecipientDocument: "39053344705", Relationship: ramp.RelationshipOther,
	}
	for _, out := range []string{
		reg.String(), fmt.Sprintf("%v", reg), fmt.Sprintf("%+v", reg), fmt.Sprintf("%#v", reg),
		fmt.Sprintf("%s", &reg), fmt.Sprintf("%v", &reg),
	} {
		assert.NotContains(t, out, "ana@example.com")
		assert.NotContains(t, out, "Ana Souza")
		assert.NotContains(t, out, "39053344705")
		assert.Contains(t, out, "xip-pix-abc")
	}
}

func TestPixAccountNameIsDeterministicAndHoldsNoUserData(t *testing.T) {
	a := ramp.PixAccountName("keyhash1", "dochash1")
	assert.Equal(t, a, ramp.PixAccountName("keyhash1", "dochash1"))
	assert.NotEqual(t, a, ramp.PixAccountName("keyhash1", "dochash2"), "the same key under another document is another account")
	assert.NotEqual(t, a, ramp.PixAccountName("keyhash2", "dochash1"))
	assert.Regexp(t, `^xip-pix-[0-9a-f]{20}$`, a)
}
