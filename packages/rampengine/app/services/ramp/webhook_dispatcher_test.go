package ramp_test

import (
	"errors"
	"testing"

	contractsqueue "github.com/goravel/framework/contracts/queue"
	mocksqueue "github.com/goravel/framework/mocks/queue"
	"github.com/goravel/framework/testing/mock"
	"github.com/stretchr/testify/require"

	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
)

type processJob struct{}

func (processJob) Signature() string   { return "process_webhook_event" }
func (processJob) Handle(...any) error { return nil }

func TestQueueDispatcherQueuesTheEventID(t *testing.T) {
	queue := mock.Factory().Queue()
	pending := &mocksqueue.PendingJob{}
	queue.On("Job", processJob{}, []contractsqueue.Arg{{Value: uint64(9), Type: "uint64"}}).Return(pending).Once()
	pending.On("Dispatch").Return(nil).Once()

	err := rampsvc.NewQueueDispatcher(func() (contractsqueue.Job, error) { return processJob{}, nil }).Dispatch(9)

	require.NoError(t, err)
	pending.AssertExpectations(t)
}

func TestQueueDispatcherWithoutAJobFails(t *testing.T) {
	err := rampsvc.NewQueueDispatcher(func() (contractsqueue.Job, error) { return nil, errors.New("not bound") }).Dispatch(9)

	require.Error(t, err)
}
