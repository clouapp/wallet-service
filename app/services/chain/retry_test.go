package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/macrowallets/waas/pkg/httpclient"
)

func TestKnown_Failure_IsATimeoutBeforeSendOrServerErrorOrRateLimit(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, beforeSend := httpclient.NewClient(time.Second).Do(ctx, httpclient.Request{
		Method: httpclient.MethodGet,
		URL:    "http://127.0.0.1:1",
	})
	if !KnownFailure(beforeSend) || errors.Is(beforeSend, ErrUnknownOutcome) {
		t.Fatalf("timeout before send: %v", beforeSend)
	}
	if got := ClassifyBroadcast(beforeSend); got != beforeSend {
		t.Fatalf("timeout before send was rewritten: %v", got)
	}

	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		err := FromProviderHTTP(status, "down")
		if !KnownFailure(err) || errors.Is(err, ErrUnknownOutcome) {
			t.Fatalf("HTTP %d: a read answered %d may be retried: %v", status, status, err)
		}
	}

	limited := errors.Join(errors.New("rpc call"), ErrRateLimited)
	if !KnownFailure(limited) {
		t.Fatal("rate limit was not a known failure")
	}
	if got := ClassifyBroadcast(limited); got != limited {
		t.Fatalf("rate limit was rewritten: %v", got)
	}
}

func TestClassify_Broadcast_LeavesADefiniteRejection(t *testing.T) {
	for _, err := range []error{
		FromProviderHTTP(http.StatusBadRequest, "rejected"),
		FromProviderHTTP(http.StatusNotFound, "missing"),
		errors.New("nonce too low"),
	} {
		if KnownFailure(err) {
			t.Fatalf("definite rejection classified as known: %v", err)
		}
		if got := ClassifyBroadcast(err); got != err || errors.Is(got, ErrUnknownOutcome) {
			t.Fatalf("definite rejection rewritten: %v", got)
		}
	}
	if got := ClassifyBroadcast(nil); got != nil {
		t.Fatalf("nil became %v", got)
	}
}

func TestClassify_Broadcast_MarksAnUnansweredSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("closed server was dialed")
	}))
	endpoint := srv.URL
	srv.Close()

	_, err := httpclient.NewClient(time.Second).Do(context.Background(), httpclient.Request{
		Method:  httpclient.MethodPost,
		URL:     endpoint,
		Body:    []byte{0x01},
		HasBody: true,
	})
	if err == nil || !httpclient.IsRoundtrip(err) {
		t.Fatalf("roundtrip err %v", err)
	}
	classified := ClassifyBroadcast(Unavailable(err))
	if !errors.Is(classified, ErrUnknownOutcome) || !errors.Is(classified, ErrProviderUnavailable) {
		t.Fatalf("classified %v", classified)
	}
	if KnownFailure(classified) {
		t.Fatal("unknown broadcast result must not be retried")
	}
	if classified.Error() != ErrProviderUnavailable.Error() {
		t.Fatalf("error text %q", classified.Error())
	}
}

func TestUnknown_Outcome_HidesAServerErrorFromRetry(t *testing.T) {
	err := UnknownOutcome(FromProviderHTTP(http.StatusBadGateway, "maybe accepted"))
	if !errors.Is(err, ErrUnknownOutcome) || KnownFailure(err) {
		t.Fatalf("known=%v err=%v", KnownFailure(err), err)
	}
	if err.Error() != ErrProviderUnavailable.Error() {
		t.Fatalf("error text %q", err.Error())
	}
}

func TestClassify_Broadcast_AServerErrorMayHaveRelayedTheTransaction(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		err := FromProviderHTTP(status, "upstream timed out")
		got := ClassifyBroadcast(err)
		if !errors.Is(got, ErrUnknownOutcome) {
			t.Fatalf("HTTP %d on a broadcast must be an unknown outcome (a gateway may have relayed it): %v", status, got)
		}
		if KnownFailure(got) {
			t.Fatalf("HTTP %d: an unknown outcome is not safe to send again", status)
		}
		var failure *Failure
		if !errors.As(got, &failure) || failure.Status != status {
			t.Fatalf("HTTP %d: the provider failure stays on the chain: %v", status, got)
		}
	}
}

func TestClassify_Broadcast_AnAnswerThatCannotBeReadIsAnUnknownOutcome(t *testing.T) {
	var syntax *json.SyntaxError
	syntaxErr := json.Unmarshal([]byte("<html>gateway</html>"), &struct{}{})
	if !errors.As(syntaxErr, &syntax) {
		t.Fatalf("setup: %v", syntaxErr)
	}
	var hash string
	typeErr := json.Unmarshal([]byte(`{"hash":"0x1"}`), &hash)
	for name, err := range map[string]error{
		"html body":    fmt.Errorf("unmarshal eth_sendRawTransaction response: %w", syntaxErr),
		"wrong result": typeErr,
	} {
		if got := ClassifyBroadcast(err); !errors.Is(got, ErrUnknownOutcome) {
			t.Fatalf("%s: a 2xx answer that cannot be read may have been accepted: %v", name, got)
		}
	}
}

func TestClassify_Broadcast_LeavesARateLimitAndARequestThatNeverLeft(t *testing.T) {
	_, buildErr := httpclient.NewClient(time.Second).Do(context.Background(), httpclient.Request{
		Method: httpclient.MethodPost,
		URL:    "http://[::1]:namedport",
	})
	if buildErr == nil || !httpclient.IsBuild(buildErr) {
		t.Fatalf("setup: build err %v", buildErr)
	}
	for name, err := range map[string]error{
		"rate limited":   FromProviderHTTP(http.StatusTooManyRequests, "slow down"),
		"never sent":     buildErr,
		"wrapped limit":  fmt.Errorf("eth_sendRawTransaction: %w", ErrRateLimited),
		"never sent too": Unavailable(buildErr),
	} {
		got := ClassifyBroadcast(err)
		if errors.Is(got, ErrUnknownOutcome) || got.Error() != err.Error() || !errors.Is(got, err) {
			t.Fatalf("%s: a broadcast the network never saw became %v", name, got)
		}
	}
}
