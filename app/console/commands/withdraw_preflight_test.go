package commands

import (
	"strings"
	"testing"
)

const preflightTestWalletID = "d6a8ce92-b637-44c2-a9f1-774344802e1a"

func TestReadPreflightRequest_AcceptsWithdrawalAndConsolidation(t *testing.T) {
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

func TestReadPreflightRequest_RejectsIncompleteRequests(t *testing.T) {
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

func TestReadPreflightRequest_ErrorsNeverEchoThePassphrase(t *testing.T) {
	_, err := readPreflightRequest(strings.NewReader(`{"mode":"withdrawal","wallet_id":"` + preflightTestWalletID +
		`","asset":"ETH","amount":"x","to":"0xabc","passphrase":"secret-passphrase-value"}`))
	if err == nil || strings.Contains(err.Error(), "secret-passphrase-value") {
		t.Fatalf("error %v", err)
	}
}
