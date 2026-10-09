package sweep

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/google/uuid"
)

type planRecorder struct {
	Service
	walletID uuid.UUID
	asset    string
	amount   *big.Int
	to       string
	caller   uuid.UUID
	plan     *Plan
	err      error
}

func (p *planRecorder) PlanForWithdrawal(_ context.Context, walletID uuid.UUID, asset string, amount *big.Int, to string, caller uuid.UUID) (*Plan, error) {
	p.walletID, p.asset, p.amount, p.to, p.caller = walletID, asset, amount, to, caller
	return p.plan, p.err
}

func TestPreview_PlansTheAmountWithoutADestination(t *testing.T) {
	plan := &Plan{Strategy: StrategyDirectFromBase}
	sweeps := &planRecorder{plan: plan}
	wallet, caller := uuid.New(), uuid.New()

	got, err := Preview(context.Background(), sweeps, PreviewInput{WalletID: wallet, Asset: "eth", Amount: "1000000000000000000", CallerAccountID: caller})

	if err != nil || got != plan {
		t.Fatalf("plan = %v, err = %v", got, err)
	}
	if sweeps.walletID != wallet || sweeps.asset != "eth" || sweeps.caller != caller {
		t.Fatalf("call = %v %q %v", sweeps.walletID, sweeps.asset, sweeps.caller)
	}
	if sweeps.amount.String() != "1000000000000000000" || sweeps.to != "" {
		t.Fatalf("amount = %v, destination = %q", sweeps.amount, sweeps.to)
	}
}

func TestPreview_RefusesWhatIsNotAWholeNumberOfBaseUnits(t *testing.T) {
	for _, amount := range []string{"", "abc", "1.5", "1e3", "0x10", " 1"} {
		sweeps := &planRecorder{}

		_, err := Preview(context.Background(), sweeps, PreviewInput{Amount: amount})

		if !errors.Is(err, ErrInvalidAmount) {
			t.Fatalf("amount %q: err = %v", amount, err)
		}
		if sweeps.amount != nil {
			t.Fatalf("amount %q reached the planner", amount)
		}
	}
}

func TestPreview_PassesThePlannerFailure(t *testing.T) {
	boom := errors.New("planner down")

	_, err := Preview(context.Background(), &planRecorder{err: boom}, PreviewInput{Amount: "1"})

	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}
