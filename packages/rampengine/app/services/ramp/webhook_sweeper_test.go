package ramp_test

import (
	"errors"
	"testing"
	"time"

	mockslog "github.com/goravel/framework/mocks/log"
	"github.com/goravel/framework/testing/mock"
	"github.com/stretchr/testify/assert"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

type sweeperHarness struct {
	sweeper    *rampsvc.WebhookSweeper
	events     *mocks.MockWebhookEventRepository
	dispatcher *mocks.MockEventDispatcher
}

func newSweeperHarness(t *testing.T) *sweeperHarness {
	t.Helper()
	h := &sweeperHarness{
		events:     mocks.NewMockWebhookEventRepository(t),
		dispatcher: mocks.NewMockEventDispatcher(t),
	}
	h.sweeper = rampsvc.NewWebhookSweeper(h.events, h.dispatcher)
	return h
}

func unprocessedEvents(ids ...uint) []models.WebhookEvent {
	events := make([]models.WebhookEvent, len(ids))
	for i, id := range ids {
		events[i].ID = id
		events[i].EventType = "transfer.updated"
	}
	return events
}

func TestWebhookSweeper_Sweep_ListsOnlyEventsOlderThanTheWindow(t *testing.T) {
	h := newSweeperHarness(t)
	var cutoff time.Time
	h.events.EXPECT().FindUnprocessed(testifymock.Anything, 100).
		RunAndReturn(func(olderThan time.Time, _ int) ([]models.WebhookEvent, error) {
			cutoff = olderThan
			return nil, nil
		}).Once()

	_, err := h.sweeper.Sweep(5*time.Minute, 100)

	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(-5*time.Minute), cutoff, time.Second)
}

func TestWebhookSweeper_Sweep_DispatchesEveryUnprocessedEvent(t *testing.T) {
	h := newSweeperHarness(t)
	h.events.EXPECT().FindUnprocessed(testifymock.Anything, 100).Return(unprocessedEvents(3, 4), nil).Once()
	h.dispatcher.EXPECT().Dispatch(uint(3)).Return(nil).Once()
	h.dispatcher.EXPECT().Dispatch(uint(4)).Return(nil).Once()

	report, err := h.sweeper.Sweep(5*time.Minute, 100)

	require.NoError(t, err)
	assert.Equal(t, &rampsvc.WebhookSweepReport{Scanned: 2, Dispatched: 2}, report)
}

// A queue that refuses one event must not strand the rest of the batch: the
// refused row stays unprocessed and the next run picks it up.
func TestWebhookSweeper_Sweep_ARefusedDispatchIsLoggedAndTheSweepGoesOn(t *testing.T) {
	logger := &mockslog.Log{}
	mock.Factory().App().On("MakeLog").Return(logger)
	dispatchErr := errors.New("redis: connection refused")
	logger.EXPECT().Errorf("ramp webhook sweep: dispatch event %d (%s): %v", uint(3), "transfer.updated", dispatchErr).Once()

	h := newSweeperHarness(t)
	h.events.EXPECT().FindUnprocessed(testifymock.Anything, 100).Return(unprocessedEvents(3, 4), nil).Once()
	h.dispatcher.EXPECT().Dispatch(uint(3)).Return(dispatchErr).Once()
	h.dispatcher.EXPECT().Dispatch(uint(4)).Return(nil).Once()

	report, err := h.sweeper.Sweep(5*time.Minute, 100)

	require.NoError(t, err)
	assert.Equal(t, &rampsvc.WebhookSweepReport{Scanned: 2, Dispatched: 1}, report)
	logger.AssertExpectations(t)
}

func TestWebhookSweeper_Sweep_ListFailureIsReturned(t *testing.T) {
	h := newSweeperHarness(t)
	cause := errors.New("connection refused")
	h.events.EXPECT().FindUnprocessed(testifymock.Anything, 100).Return(nil, cause).Once()

	report, err := h.sweeper.Sweep(5*time.Minute, 100)

	require.ErrorIs(t, err, cause)
	assert.Nil(t, report)
}
