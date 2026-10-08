package e2e

import (
	"context"
	"strings"
	"testing"
)

type recordedProcess struct {
	binary string
	args   []string
}

func outboundServices(stdout string, calls *[]recordedProcess) DockerServices {
	return DockerServices{
		WalletsDBContainer: "waas-postgres",
		Run: func(_ context.Context, binary string, args []string, _ string, _ []string, _ []byte) (ProcessResult, error) {
			*calls = append(*calls, recordedProcess{binary: binary, args: args})
			return ProcessResult{Stdout: []byte(stdout)}, nil
		},
	}
}

func TestOutbound_Matches_OnlyComparesTransfersOfTheSameAsset(t *testing.T) {
	var calls []recordedProcess
	services := outboundServices("id-1 confirmed 0xab\n", &calls)

	matches, err := services.OutboundMatches(context.Background(), testWalletID, testTo, "USDC", "400000")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0] != "id-1 confirmed 0xab" {
		t.Fatalf("matches %v", matches)
	}
	if len(calls) != 1 || calls[0].binary != dockerBinary {
		t.Fatalf("calls %v", calls)
	}
	sql := calls[0].args[len(calls[0].args)-1]
	for _, fragment := range []string{
		"wallet_id = '" + testWalletID + "'",
		"direction = 'outbound'",
		"lower(to_address) = lower('" + testTo + "')",
		"upper(asset) = 'USDC'",
		"amount = '400000'",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("query misses %q: %s", fragment, sql)
		}
	}
	if !strings.Contains(strings.Join(calls[0].args, " "), "-d "+E2EDatabase) {
		t.Fatalf("query does not target %s: %v", E2EDatabase, calls[0].args)
	}
}

func TestOutbound_Matches_RefusesUnsafeInputsBeforeQuerying(t *testing.T) {
	cases := map[string][4]string{
		"lower-case asset":       {testWalletID, testTo, "usdc", "1"},
		"asset with a quote":     {testWalletID, testTo, "USDC'--", "1"},
		"empty asset":            {testWalletID, testTo, "", "1"},
		"wallet id not a uuid":   {"wallet", testTo, "ETH", "1"},
		"address with a quote":   {testWalletID, "0x11'11111111111111111111111111111111", "ETH", "1"},
		"amount with a fraction": {testWalletID, testTo, "ETH", "0.5"},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			var calls []recordedProcess
			services := outboundServices("", &calls)
			if _, err := services.OutboundMatches(context.Background(), input[0], input[1], input[2], input[3]); err == nil {
				t.Fatal("expected a validation error")
			}
			if len(calls) != 0 {
				t.Fatalf("queried vault_test with an invalid input: %v", calls)
			}
		})
	}
}
