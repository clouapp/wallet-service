package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func TestClassify_Broadcast_ARefusedConnectionWasNeverSent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("closed server was dialed")
	}))
	endpoint := srv.URL
	srv.Close()

	for name, url := range map[string]string{"refused": endpoint, "no such host": "http://wallet-rpc.invalid"} {
		_, err := httpclient.NewClient(time.Second).Do(context.Background(), httpclient.Request{
			Method: httpclient.MethodPost, URL: url, Body: []byte{0x01}, HasBody: true,
		})
		if err == nil {
			t.Fatalf("%s: setup: no error", name)
		}
		if !RequestNotSent(err) {
			t.Fatalf("%s: a connection that was never made is a request never sent: %v", name, err)
		}
		classified := ClassifyBroadcast(Unavailable(err))
		if errors.Is(classified, ErrUnknownOutcome) || !errors.Is(classified, ErrProviderUnavailable) {
			t.Fatalf("%s: a broadcast that reached no node is not an unknown outcome: %v", name, classified)
		}
	}
}

func TestClassify_Broadcast_AConnectionDroppedAfterTheSendIsAnUnknownOutcome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(srv.Close)

	_, err := httpclient.NewClient(time.Second).Do(context.Background(), httpclient.Request{
		Method: httpclient.MethodPost, URL: srv.URL, Body: []byte{0x01}, HasBody: true,
	})
	if err == nil || RequestNotSent(err) {
		t.Fatalf("setup: the request was sent: %v", err)
	}
	classified := ClassifyBroadcast(Unavailable(err))
	if !errors.Is(classified, ErrUnknownOutcome) || KnownFailure(classified) {
		t.Fatalf("a node that read the bytes may have relayed them: %v", classified)
	}
	if classified.Error() != ErrProviderUnavailable.Error() {
		t.Fatalf("error text %q", classified.Error())
	}
}

func TestClassify_Broadcast_ACallMarkedNotSentStaysAFailureWhateverItCarries(t *testing.T) {
	identityCheck := FromProviderHTTP(http.StatusBadGateway, "the fallback's eth_chainId failed")
	err := NotSent(identityCheck)
	if !RequestNotSent(err) || !errors.Is(err, ErrNotSent) || !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("NotSent keeps its cause on the chain: %v", err)
	}
	if got := ClassifyBroadcast(err); errors.Is(got, ErrUnknownOutcome) || !errors.Is(got, ErrNotSent) || got.Error() != err.Error() {
		t.Fatalf("no broadcast exchange happened, so the 502 says nothing about the transaction: %v", got)
	}
	var twice *notSent
	if !errors.As(NotSent(err), &twice) || errors.As(twice.cause, new(*notSent)) || !errors.Is(NotSent(nil), ErrNotSent) {
		t.Fatal("NotSent is idempotent and names the sentinel without a cause")
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
