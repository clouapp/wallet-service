package ozav

import (
	"testing"

	"github.com/stretchr/testify/require"

	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// Every key the client may send has a label here; a key added to the
// vocabulary without a label would reach Ozav as an empty string.
func TestEveryVocabularyKeyHasALabel(t *testing.T) {
	for _, key := range kycsvc.SourceOfFundsKeys {
		label, err := sourceOfFundsLabel(key)
		require.NoError(t, err, key)
		require.NotEmpty(t, label, key)
	}
	for _, key := range kycsvc.AccountPurposeKeys {
		label, err := accountPurposeLabel(key)
		require.NoError(t, err, key)
		require.NotEmpty(t, label, key)
	}
	_, err := sourceOfFundsLabel("salary")
	require.Error(t, err, "an unknown key is refused, never forwarded")
	label, err := sourceOfFundsLabel("")
	require.NoError(t, err, "a field the user left out stays out")
	require.Empty(t, label)
}
