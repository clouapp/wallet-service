package ramp

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBankAccountNameIsDeterministicPerRevisionAndRail(t *testing.T) {
	id := "6f1c2a9e-1111-4222-8333-944455556666"
	assert.Equal(t, "xip-"+id+"-r3-ach", BankAccountName(id, 3, RailACHPayout))
	assert.Equal(t, "xip-"+id+"-r3-wire", BankAccountName(id, 3, RailWirePayout))
	assert.NotEqual(t, BankAccountName(id, 3, RailACHPayout), BankAccountName(id, 4, RailACHPayout))
}

func TestBankAccountRegistrationNeverPrintsTheNumbers(t *testing.T) {
	reg := BeneficiaryAccountRegistration{Name: "n", Rail: RailACHPayout, BeneficiaryName: "Ana Souza",
		RoutingNumber: "021000021", AccountNumber: "123456789", AddressLine1: "350 Fifth Avenue"}
	for _, verb := range []string{"%v", "%+v", "%s", "%#v"} {
		out := fmt.Sprintf(verb, reg)
		assert.NotContains(t, out, "123456789", verb)
		assert.NotContains(t, out, "021000021", verb)
		assert.NotContains(t, out, "Ana Souza", verb)
		assert.NotContains(t, out, "350 Fifth", verb)
	}
}

func TestBankAccountEchoMismatchIsAProviderFailure(t *testing.T) {
	assert.True(t, errors.Is(ErrBankAccountEchoMismatch, ErrProviderFailed))
}
