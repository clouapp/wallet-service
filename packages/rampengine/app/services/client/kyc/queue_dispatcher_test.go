package kyc

import (
	"errors"
	"testing"
	"time"

	contractsqueue "github.com/goravel/framework/contracts/queue"
	mocksqueue "github.com/goravel/framework/mocks/queue"
	"github.com/goravel/framework/testing/mock"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Only the sync connection runs the job inside Dispatch. Reading any other one
// as inline would make a documents submission give up on a customer the
// queued application is about to create.
func TestQueueDispatcherRunsInlineOnlyOnTheSyncConnection(t *testing.T) {
	for connection, inline := range map[string]bool{"sync": true, "database": false} {
		config := mock.Factory().Config()
		config.EXPECT().GetString("queue.default").Return(connection).Once()

		require.Equal(t, inline, NewQueueDispatcher(nil).RunsInline(), connection)
		config.AssertExpectations(t)
	}
}

// The job decodes these three routing args by position; the documents stay
// sealed in staging and never travel through the queue.
func TestQueueDispatcherQueuesOnlyTheRoutingArgs(t *testing.T) {
	args := []contractsqueue.Arg{
		{Value: "stg_1", Type: "string"}, {Value: uint64(7), Type: "uint64"}, {Value: "bridge", Type: "string"},
	}
	for name, delayed := range map[string]bool{"now": false, "later": true} {
		t.Run(name, func(t *testing.T) {
			queue := mock.Factory().Queue()
			job := mocksqueue.NewJob(t)
			pending := mocksqueue.NewPendingJob(t)
			queue.EXPECT().Job(job, args).Return(pending).Once()
			if delayed {
				pending.EXPECT().Delay(testifymock.MatchedBy(func(at time.Time) bool {
					return at.After(time.Now())
				})).Return(pending).Once()
			}
			pending.EXPECT().Dispatch().Return(nil).Once()
			dispatcher := NewQueueDispatcher(func() (contractsqueue.Job, error) { return job, nil })

			if delayed {
				require.NoError(t, dispatcher.DispatchAfter("stg_1", 7, "bridge", time.Second))
			} else {
				require.NoError(t, dispatcher.Dispatch("stg_1", 7, "bridge"))
			}
			queue.AssertExpectations(t)
		})
	}
}

func TestQueueDispatcherReportsAJobItCannotResolve(t *testing.T) {
	dispatcher := NewQueueDispatcher(func() (contractsqueue.Job, error) { return nil, errors.New("not registered") })

	require.Error(t, dispatcher.Dispatch("stg_1", 7, "bridge"))
	require.Error(t, dispatcher.DispatchAfter("stg_1", 7, "bridge", time.Second))
}
