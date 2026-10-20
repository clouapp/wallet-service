//go:build xrp_stream_live

package xrp

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
)

// TestLive_Stream_Altnet reads, without sending anything, the public altnet
// WebSocket (XRP_STREAM_LIVE_URL, default wss://s.altnet.rippletest.net:51233):
// the ledger stream, an account subscription and account_tx of
// XRP_STREAM_LIVE_ACCOUNT from XRP_STREAM_LIVE_FROM (default: 2000 ledgers back)
// to the validated ledger.
//
//	go test ./app/adapters/chain/xrp/ -tags xrp_stream_live -run TestLive_Stream -v
func TestLive_Stream_Altnet(t *testing.T) {
	url := os.Getenv("XRP_STREAM_LIVE_URL")
	if url == "" {
		url = "wss://s.altnet.rippletest.net:51233"
	}
	account := os.Getenv("XRP_STREAM_LIVE_ACCOUNT")
	if account == "" {
		account = "rai1iQmERbDPvfuk31XVsYs6ZXeA8QRZnC"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	session, err := NewStreamDialer(StreamConfig{ChainIDStr: models.ChainTXRP, NativeSymbol: models.NativeXRP, URL: url}).Dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	ledger, err := session.SubscribeLedgers(ctx)
	if err != nil || ledger == 0 {
		t.Fatalf("validated ledger %d err %v", ledger, err)
	}
	if err := session.SubscribeAccounts(ctx, []string{account}); err != nil {
		t.Fatal(err)
	}
	from := ledger - min(ledger, 2000)
	if raw := os.Getenv("XRP_STREAM_LIVE_FROM"); raw != "" {
		if from, err = strconv.ParseUint(raw, 10, 64); err != nil {
			t.Fatal(err)
		}
	}
	deposits, err := session.AccountDeposits(ctx, account, from, ledger)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("subscribed at ledger %d; %d deposits to %s since ledger %d", ledger, len(deposits), account, from)
	for _, deposit := range deposits {
		tag := "none"
		if deposit.DestinationTag != nil {
			tag = strconv.FormatUint(uint64(*deposit.DestinationTag), 10)
		}
		t.Logf("deposit %s ledger %d drops %s tag %s", deposit.TxHash, deposit.BlockNumber, deposit.Amount, tag)
	}
	for heard := 0; heard < 2; {
		event, err := session.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Ledger > ledger {
			heard++
			t.Logf("validated ledger %d, %d deposits", event.Ledger, len(event.Deposits))
		}
	}
}
