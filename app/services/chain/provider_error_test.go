package chain

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestProvider_Failure_HidesTheProviderSentence(t *testing.T) {
	const raw = "execution reverted: secret-node-detail"
	err := FromProviderHTTP(http.StatusBadGateway, raw)

	if err.Error() != ErrProviderUnavailable.Error() || errors.Is(err, ErrNotFound) {
		t.Fatalf("error %v", err)
	}
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatal("errors.Is missed provider unavailable")
	}
	if ClientText(fmt.Errorf("broadcast: %w", err)) != ErrProviderUnavailable.Error() {
		t.Fatalf("client text %q", ClientText(err))
	}
	if cause := CauseText(err); cause == "" || !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("cause %q", cause)
	}
	if ClientText(err) == raw || err.Error() == raw {
		t.Fatalf("client text included the provider sentence: %v", err)
	}
	if CauseText(err) == "" {
		t.Fatal("log cause was dropped")
	}
}

func TestKind_Of_MapsTheAdapterCases(t *testing.T) {
	cases := []struct {
		status  int
		message string
		want    error
	}{
		{http.StatusGatewayTimeout, "upstream html", ErrProviderUnavailable},
		{http.StatusNotFound, "missing", ErrNotFound},
		{http.StatusTooManyRequests, "slow down", ErrRateLimited},
		{0, "invalid address", ErrInvalidAddress},
		{0, "insufficient funds for gas * price + value", ErrInsufficientFunds},
		{0, "nonce too low", ErrNonce},
		{0, "replacement transaction underpriced", ErrFee},
		{0, "could not find account", ErrNotFound},
		{0, "execution reverted", nil},
	}
	for _, tc := range cases {
		got := KindOf(tc.status, tc.message)
		if !errors.Is(got, tc.want) && got != tc.want {
			t.Fatalf("status %d %q: got %v want %v", tc.status, tc.message, got, tc.want)
		}
	}
	if KindOrProvider(0, "execution reverted") != ErrProvider {
		t.Fatal("an unnamed provider sentence must stay a typed provider error")
	}
}
