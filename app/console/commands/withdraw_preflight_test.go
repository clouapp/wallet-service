package commands

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/pkg/types"
)

const preflightTestWalletID = "d6a8ce92-b637-44c2-a9f1-774344802e1a"

func TestRead_PreflightRequest_AcceptsWithdrawalAndConsolidation(t *testing.T) {
	withdrawal, err := readPreflightRequest(strings.NewReader(`{"mode":"withdrawal","wallet_id":"` + preflightTestWalletID +
		`","asset":"ETH","amount":"1000000000000000","to":" 0xe3c1d504c4633521197ffa736b2c9a7f93478076 ","passphrase":"twelve-chars-or-more"}`))
	if err != nil {
		t.Fatal(err)
	}
	if withdrawal.To != "0xe3c1d504c4633521197ffa736b2c9a7f93478076" {
		t.Fatalf("destination not trimmed: %q", withdrawal.To)
	}
	if _, err := readPreflightRequest(strings.NewReader(`{"mode":"consolidation","wallet_id":"` + preflightTestWalletID +
		`","asset":"ETH","passphrase":"twelve-chars-or-more"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestRead_PreflightRequest_RejectsIncompleteRequests(t *testing.T) {
	valid := map[string]string{
		"mode": "withdrawal", "wallet_id": preflightTestWalletID, "asset": "ETH",
		"amount": "1000", "to": "0xe3c1d504c4633521197ffa736b2c9a7f93478076", "passphrase": "twelve-chars-or-more",
	}
	cases := map[string][2]string{
		"unknown mode":      {"mode", "broadcast"},
		"wallet not a uuid": {"wallet_id", "wallet-1"},
		"no asset":          {"asset", ""},
		"zero amount":       {"amount", "0"},
		"negative amount":   {"amount", "-5"},
		"decimal amount":    {"amount", "0.001"},
		"no destination":    {"to", ""},
		"short passphrase":  {"passphrase", "short"},
	}
	for name, override := range cases {
		fields := make([]string, 0, len(valid))
		for key, value := range valid {
			if key == override[0] {
				value = override[1]
			}
			fields = append(fields, `"`+key+`":"`+value+`"`)
		}
		if _, err := readPreflightRequest(strings.NewReader("{" + strings.Join(fields, ",") + "}")); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := readPreflightRequest(strings.NewReader("not json")); err == nil {
		t.Error("non-JSON request: expected an error")
	}
}

func TestRead_PreflightRequest_ErrorsNeverEchoThePassphrase(t *testing.T) {
	_, err := readPreflightRequest(strings.NewReader(`{"mode":"withdrawal","wallet_id":"` + preflightTestWalletID +
		`","asset":"ETH","amount":"x","to":"0xabc","passphrase":"secret-passphrase-value"}`))
	if err == nil || strings.Contains(err.Error(), "secret-passphrase-value") {
		t.Fatalf("error %v", err)
	}
}

type preflightSweepFake struct {
	sweep.Service
	planned         bool
	consolidatedFor uuid.UUID
	result          *sweep.Preflight
}

func (f *preflightSweepFake) PlanForWithdrawal(context.Context, uuid.UUID, string, *big.Int, string, uuid.UUID) (*sweep.Plan, error) {
	f.planned = true
	return &sweep.Plan{}, nil
}

func (f *preflightSweepFake) PreflightWithdrawal(context.Context, *sweep.Plan, string, string) (*sweep.Preflight, error) {
	return f.result, nil
}

func (f *preflightSweepFake) PreflightConsolidation(_ context.Context, walletID uuid.UUID, _ string, _ string) (*sweep.Preflight, error) {
	f.consolidatedFor = walletID
	return f.result, nil
}

type preflightChainFake struct {
	types.Chain
	valid bool
}

func (c preflightChainFake) ValidateAddress(string) bool { return c.valid }

type preflightChainsFake struct{ chain types.Chain }

func (c preflightChainsFake) Chain(string) (types.Chain, error) { return c.chain, nil }

func newPreflightRunner(sweepService sweep.Service, valid bool) preflightRunner {
	return preflightRunner{
		sweep:  sweepService,
		chains: preflightChainsFake{chain: preflightChainFake{valid: valid}},
		walletChain: func(context.Context, uuid.UUID) (string, error) {
			return "eth", nil
		},
	}
}

func preflightTestRequest(mode string) preflightRequest {
	return preflightRequest{Mode: mode, WalletID: preflightTestWalletID, Asset: "ETH", Amount: "1000", To: "0xabc", Passphrase: "twelve-chars-or-more"}
}

func TestPreflight_Runner_RefusesASweepServiceThatCannotPreflight(t *testing.T) {
	runner := newPreflightRunner(struct{ sweep.Service }{}, true)

	_, err := runner.preflight(context.Background(), preflightTestRequest(preflightModeWithdrawal))

	if err == nil || !strings.Contains(err.Error(), "does not support preflight") {
		t.Fatalf("err = %v", err)
	}
}

func TestPreflight_Runner_RefusesABadDestinationBeforePlanning(t *testing.T) {
	fake := &preflightSweepFake{result: &sweep.Preflight{}}

	_, err := newPreflightRunner(fake, false).preflight(context.Background(), preflightTestRequest(preflightModeWithdrawal))

	if err == nil || !strings.Contains(err.Error(), "invalid address for chain eth") {
		t.Fatalf("err = %v", err)
	}
	if fake.planned {
		t.Fatal("a withdrawal to an invalid address was planned")
	}
}

func TestPreflight_Runner_StopsWhenTheWalletIsNotFound(t *testing.T) {
	fake := &preflightSweepFake{result: &sweep.Preflight{}}
	runner := newPreflightRunner(fake, true)
	runner.walletChain = func(_ context.Context, id uuid.UUID) (string, error) {
		return "", fmt.Errorf("wallet %s not found", id)
	}

	_, err := runner.preflight(context.Background(), preflightTestRequest(preflightModeWithdrawal))

	if err == nil || !strings.Contains(err.Error(), "not found") || fake.planned {
		t.Fatalf("err = %v, planned = %v", err, fake.planned)
	}
}

func TestPreflight_Runner_PlansAWithdrawalAndConsolidatesWithoutALookup(t *testing.T) {
	fake := &preflightSweepFake{result: &sweep.Preflight{Asset: "ETH"}}
	runner := newPreflightRunner(fake, true)

	output, err := runner.preflight(context.Background(), preflightTestRequest(preflightModeWithdrawal))
	if err != nil || !fake.planned || output.Mode != preflightModeWithdrawal || output.Broadcast {
		t.Fatalf("withdrawal: output %+v, planned %v, err %v", output, fake.planned, err)
	}

	runner.walletChain = func(context.Context, uuid.UUID) (string, error) {
		t.Fatal("a consolidation looked the wallet up")
		return "", nil
	}
	if _, err := runner.preflight(context.Background(), preflightTestRequest(preflightModeConsolidation)); err != nil {
		t.Fatal(err)
	}
	if fake.consolidatedFor.String() != preflightTestWalletID {
		t.Fatalf("consolidated wallet %s", fake.consolidatedFor)
	}
}
