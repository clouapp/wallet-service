package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/chain"
)

const (
	primaryURL  = "https://primary.example"
	fallbackURL = "https://fallback.example"
	thirdURL    = "https://third.example"
)

// scriptedExchange answers each endpoint as the test decides and records the
// attempts it was given, in order.
type scriptedExchange struct {
	mu       sync.Mutex
	answers  map[string]func() (Outcome, error)
	attempts []Attempt
}

func newScriptedExchange(answers map[string]func() (Outcome, error)) *scriptedExchange {
	return &scriptedExchange{answers: answers}
}

func (s *scriptedExchange) exchange(_ context.Context, at Attempt) (Outcome, error) {
	s.mu.Lock()
	s.attempts = append(s.attempts, at)
	answer := s.answers[at.URL]
	s.mu.Unlock()
	if answer == nil {
		return OutcomeAnswer, nil
	}
	return answer()
}

func (s *scriptedExchange) urls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	urls := make([]string, 0, len(s.attempts))
	for _, at := range s.attempts {
		urls = append(urls, at.URL)
	}
	return urls
}

func answered(err error) func() (Outcome, error) {
	return func() (Outcome, error) { return OutcomeAnswer, err }
}

func unavailable(err error) func() (Outcome, error) {
	return func() (Outcome, error) { return OutcomeUnavailable, err }
}

func rateLimited() func() (Outcome, error) {
	return func() (Outcome, error) { return OutcomeRateLimited, fmt.Errorf("busy: %w", chain.ErrRateLimited) }
}

// identities answers each endpoint's network and counts the questions.
type identities struct {
	mu      sync.Mutex
	network map[string]string
	asked   map[string]int
}

func (i *identities) read(_ context.Context, rawURL string) (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.asked == nil {
		i.asked = map[string]int{}
	}
	i.asked[rawURL]++
	network, ok := i.network[rawURL]
	if !ok {
		return "", errors.New("no answer")
	}
	return network, nil
}

func (i *identities) count(rawURL string) int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.asked[rawURL]
}

func testEndpoints(network string, identity func(context.Context, string) (string, error), urls ...string) (*Endpoints, *recordingSink) {
	sink := &recordingSink{}
	endpoints := NewEndpoints(EndpointsDeps{
		URL:       urls[0],
		Fallbacks: fallbackList(urls[1:]...),
		ChainID:   "trx",
		Identity:  identity,
		Network:   network,
		Health:    sink,
	})
	clock := &testClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	endpoints.now = clock.Now
	return endpoints, sink
}

// dialRefused is a connection that was never made: the request did not leave.
func dialRefused() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
}

func TestEndpoints_Do_UsesThePrimaryWhileItAnswersAndMovesOnWhenItCannot(t *testing.T) {
	endpoints, sink := testEndpoints("", nil, primaryURL, fallbackURL)
	healthy := newScriptedExchange(nil)
	require.NoError(t, endpoints.Do(context.Background(), false, healthy.exchange))
	assert.Equal(t, []string{primaryURL}, healthy.urls())
	assert.True(t, healthy.attempts[0].Primary)
	assert.False(t, healthy.attempts[0].Last, "a fallback is left behind the primary")

	down := newScriptedExchange(map[string]func() (Outcome, error){primaryURL: unavailable(errors.New("http 502"))})
	require.NoError(t, endpoints.Do(context.Background(), false, down.exchange))
	assert.Equal(t, []string{primaryURL, fallbackURL}, down.urls())
	assert.False(t, down.attempts[1].Primary)
	assert.True(t, down.attempts[1].Last)

	health := endpoints.Health()
	require.Len(t, health, 2)
	assert.Equal(t, 1, health[0].ConsecutiveFailures)
	assert.Equal(t, 2, health[1].Position)
	assert.Positive(t, sink.count(), "a change of health is recorded")
}

func TestEndpoints_Do_AnAnswerAboutTheRequestIsReturnedWithoutAskingTheNext(t *testing.T) {
	endpoints, _ := testEndpoints("", nil, primaryURL, fallbackURL)
	refusal := errors.New("contract validate error")
	exchange := newScriptedExchange(map[string]func() (Outcome, error){primaryURL: answered(refusal)})

	err := endpoints.Do(context.Background(), false, exchange.exchange)

	require.ErrorIs(t, err, refusal)
	assert.Equal(t, []string{primaryURL}, exchange.urls())
	assert.Zero(t, endpoints.Health()[0].ConsecutiveFailures, "an answer is a healthy endpoint")
}

func TestEndpoints_Do_AnUnsupportedCallMovesOnWithoutAFailure(t *testing.T) {
	endpoints, _ := testEndpoints("", nil, primaryURL, fallbackURL)
	exchange := newScriptedExchange(map[string]func() (Outcome, error){
		primaryURL: func() (Outcome, error) { return OutcomeUnsupported, errors.New("http 404") },
	})

	require.NoError(t, endpoints.Do(context.Background(), false, exchange.exchange))
	assert.Equal(t, []string{primaryURL, fallbackURL}, exchange.urls())
	assert.Zero(t, endpoints.Health()[0].ConsecutiveFailures)
}

func TestEndpoints_Do_ABroadcastMovesOnOnlyWhenTheBytesCertainlyWereNotTaken(t *testing.T) {
	t.Run("a timeout may have handed the bytes over: the next endpoint is not asked", func(t *testing.T) {
		endpoints, _ := testEndpoints("", nil, primaryURL, fallbackURL)
		timeout := fmt.Errorf("post: %w", context.DeadlineExceeded)
		exchange := newScriptedExchange(map[string]func() (Outcome, error){primaryURL: unavailable(timeout)})

		err := endpoints.Do(context.Background(), true, exchange.exchange)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.False(t, chain.RequestNotSent(err))
		assert.Equal(t, []string{primaryURL}, exchange.urls())
	})
	t.Run("a refused connection, a rate limit and a request marked not sent move on", func(t *testing.T) {
		endpoints, _ := testEndpoints("", nil, primaryURL, fallbackURL, thirdURL)
		exchange := newScriptedExchange(map[string]func() (Outcome, error){
			primaryURL:  unavailable(dialRefused()),
			fallbackURL: unavailable(chain.NotSent(errors.New("server busy"))),
			thirdURL:    rateLimited(),
		})

		err := endpoints.Do(context.Background(), true, exchange.exchange)

		require.Error(t, err)
		assert.True(t, chain.RequestNotSent(err), "no endpoint took the bytes: the broadcast was not sent")
		assert.Equal(t, []string{primaryURL, fallbackURL, thirdURL}, exchange.urls())
	})
	t.Run("a read does not mark its failure not sent", func(t *testing.T) {
		endpoints, _ := testEndpoints("", nil, primaryURL, fallbackURL)
		exchange := newScriptedExchange(map[string]func() (Outcome, error){
			primaryURL:  unavailable(errors.New("http 502")),
			fallbackURL: unavailable(errors.New("http 503")),
		})

		err := endpoints.Do(context.Background(), false, exchange.exchange)

		require.Error(t, err)
		assert.False(t, chain.RequestNotSent(err))
	})
}

func TestEndpoints_Do_AFallbackServesOnlyOnceItAnswersTheChainsNetwork(t *testing.T) {
	t.Run("the same network serves and is asked once", func(t *testing.T) {
		ids := &identities{network: map[string]string{primaryURL: "nile", fallbackURL: "nile"}}
		endpoints, _ := testEndpoints("nile", ids.read, primaryURL, fallbackURL)
		down := map[string]func() (Outcome, error){primaryURL: unavailable(errors.New("http 502"))}

		for i := 0; i < 2; i++ {
			exchange := newScriptedExchange(down)
			require.NoError(t, endpoints.Do(context.Background(), false, exchange.exchange))
			assert.Equal(t, []string{primaryURL, fallbackURL}, exchange.urls())
		}
		assert.Equal(t, 1, ids.count(fallbackURL))
	})
	t.Run("another network is never called and is reported", func(t *testing.T) {
		ids := &identities{network: map[string]string{primaryURL: "nile", fallbackURL: "mainnet"}}
		endpoints, _ := testEndpoints("nile", ids.read, primaryURL, fallbackURL)
		exchange := newScriptedExchange(map[string]func() (Outcome, error){primaryURL: unavailable(errors.New("http 502"))})

		err := endpoints.Do(context.Background(), false, exchange.exchange)

		require.Error(t, err)
		assert.Equal(t, []string{primaryURL}, exchange.urls())
		assert.True(t, endpoints.Health()[1].WrongNetwork)
	})
	t.Run("a broadcast whose fallback could not be checked was not sent there", func(t *testing.T) {
		ids := &identities{network: map[string]string{primaryURL: "nile"}}
		endpoints, _ := testEndpoints("nile", ids.read, primaryURL, fallbackURL)
		exchange := newScriptedExchange(map[string]func() (Outcome, error){primaryURL: unavailable(dialRefused())})

		err := endpoints.Do(context.Background(), true, exchange.exchange)

		require.Error(t, err)
		assert.True(t, chain.RequestNotSent(err))
		assert.Equal(t, []string{primaryURL}, exchange.urls())
	})
}

func TestEndpoints_Do_APinnedScopeStaysOnTheEndpointThatAnsweredFirst(t *testing.T) {
	endpoints, _ := testEndpoints("", nil, primaryURL, fallbackURL)
	ctx := PinEndpoint(context.Background())
	first := newScriptedExchange(map[string]func() (Outcome, error){primaryURL: unavailable(errors.New("http 502"))})
	require.NoError(t, endpoints.Do(ctx, false, first.exchange))

	second := newScriptedExchange(map[string]func() (Outcome, error){fallbackURL: unavailable(errors.New("http 502"))})
	err := endpoints.Do(ctx, false, second.exchange)

	require.Error(t, err)
	assert.Equal(t, []string{fallbackURL}, second.urls(), "the scope never mixes the views of two endpoints")
}

func TestEndpoints_Replace_KeepsTheHealthOfAnEndpointStillListed(t *testing.T) {
	endpoints, _ := testEndpoints("", nil, primaryURL, fallbackURL)
	exchange := newScriptedExchange(map[string]func() (Outcome, error){primaryURL: unavailable(errors.New("http 502"))})
	require.NoError(t, endpoints.Do(context.Background(), false, exchange.exchange))

	endpoints.ReplaceEndpoints(primaryURL, fallbackList(thirdURL, fallbackURL))

	assert.Equal(t, primaryURL, endpoints.Endpoint())
	assert.Equal(t, []chain.RPCFallback{{URL: thirdURL, Position: 2}, {URL: fallbackURL, Position: 3}}, endpoints.Fallbacks())
	assert.Equal(t, 1, endpoints.Health()[0].ConsecutiveFailures)

	endpoints.ReplaceEndpoints("  ", nil)
	assert.Equal(t, primaryURL, endpoints.Endpoint(), "an empty primary never wipes the list")
}
