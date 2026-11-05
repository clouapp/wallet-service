package ramps

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/queue"
	"github.com/macrowallets/waas/app/models"
)

// Task payloads contain identifiers only. Identity documents stay in the
// engine's encrypted staging store with its existing expiry and scrubber.
type TaskPayload struct {
	Mode             string `json:"mode"`
	SubmissionID     string `json:"submission_id,omitempty"`
	EngineCustomerID uint   `json:"engine_customer_id,omitempty"`
	Provider         string `json:"provider,omitempty"`
	ID               uint   `json:"id,omitempty"`
}

type Task = models.RampTask
type Tasks struct{ Store TaskStore }

func (t *Tasks) Enqueue(ctx context.Context, accountID uuid.UUID, kind string, payload TaskPayload, delay time.Duration) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return t.Store.Enqueue(ctx, accountID, kind, string(encoded), delay)
}
func (t *Tasks) Claim(ctx context.Context, limit int) ([]Task, error) {
	return t.Store.Claim(ctx, limit)
}
func (t *Tasks) Finish(ctx context.Context, id uuid.UUID, cause error) error {
	return t.Store.Finish(ctx, id, cause)
}

type KycDispatcher struct {
	Tasks     *Tasks
	AccountID uuid.UUID
	Mode      string
}

func (d KycDispatcher) Dispatch(id string, customerID uint, provider string) error {
	return d.DispatchAfter(id, customerID, provider, 0)
}
func (d KycDispatcher) DispatchAfter(id string, customerID uint, provider string, delay time.Duration) error {
	return d.Tasks.Enqueue(context.Background(), d.AccountID, "kyc", TaskPayload{Mode: d.Mode, SubmissionID: id, EngineCustomerID: customerID, Provider: provider}, delay)
}
func (KycDispatcher) RunsInline() bool { return false }

type EventDispatcher struct {
	Tasks *Tasks
	Mode  string
}

func (d EventDispatcher) Dispatch(id uint) error {
	return d.Tasks.Enqueue(context.Background(), uuid.Nil, "webhook", TaskPayload{Mode: d.Mode, ID: id}, 0)
}

type settlementJob struct {
	Tasks     *Tasks
	AccountID uuid.UUID
	Mode      string
}

func (settlementJob) Signature() string { return "macro_ramp_settlement" }
func (j settlementJob) Handle(args ...any) error {
	return fmt.Errorf("ramp settlement must be staged through its native dispatcher")
}
func (j settlementJob) StageNative(args []queue.Arg) (queue.PendingJob, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("settlement requires one transaction id")
	}
	var id uint
	switch n := args[0].Value.(type) {
	case uint:
		id = n
	case uint64:
		id = uint(n)
	case int:
		id = uint(n)
	default:
		return nil, fmt.Errorf("invalid settlement transaction id")
	}
	if id == 0 {
		return nil, fmt.Errorf("invalid settlement transaction id")
	}
	return &pendingTask{job: j, payload: TaskPayload{Mode: j.Mode, ID: id}, at: time.Now()}, nil
}

type pendingTask struct {
	job     settlementJob
	payload TaskPayload
	at      time.Time
}

func (p *pendingTask) Delay(at time.Time) queue.PendingJob  { p.at = at; return p }
func (p *pendingTask) OnConnection(string) queue.PendingJob { return p }
func (p *pendingTask) OnQueue(string) queue.PendingJob      { return p }
func (p *pendingTask) DispatchSync() error                  { return p.Dispatch() }
func (p *pendingTask) Dispatch() error {
	return p.job.Tasks.Enqueue(context.Background(), p.job.AccountID, "settlement", p.payload, time.Until(p.at))
}
