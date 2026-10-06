package chain

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/macrowallets/waas/pkg/httpclient"
)

func TestKnownFailureIsATimeoutBeforeSendOrServerErrorOrRateLimit(t *testing.T) {
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
			t.Fatalf("HTTP %d: %v", status, err)
		}
		if got := ClassifyBroadcast(err); got != err {
			t.Fatalf("HTTP %d was rewritten: %v", status, got)
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

func TestClassifyBroadcastLeavesADefiniteRejection(t *testing.T) {
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

func TestClassifyBroadcastMarksAnUnansweredSend(t *testing.T) {
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

func TestUnknownOutcomeHidesAServerErrorFromRetry(t *testing.T) {
	err := UnknownOutcome(FromProviderHTTP(http.StatusBadGateway, "maybe accepted"))
	if !errors.Is(err, ErrUnknownOutcome) || KnownFailure(err) {
		t.Fatalf("known=%v err=%v", KnownFailure(err), err)
	}
	if err.Error() != ErrProviderUnavailable.Error() {
		t.Fatalf("error text %q", err.Error())
	}
}
