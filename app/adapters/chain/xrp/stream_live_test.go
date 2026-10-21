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
// the network check, an account subscription (XRP_STREAM_LIVE_ACCOUNT) and two
// validated ledgers of the stream.
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
	session, err := newTestStreamDialer(StreamConfig{ChainIDStr: models.ChainTXRP, NativeSymbol: models.NativeXRP, URL: url, IsTestnet: true}).Dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.SubscribeAccounts(ctx, []string{account}); err != nil {
		t.Fatal(err)
	}
	ledger, err := session.SubscribeLedgers(ctx)
	if err != nil || ledger == 0 {
		t.Fatalf("validated ledger %d err %v", ledger, err)
	}
	t.Logf("altnet network checked; subscribed to %s at ledger %s", account, strconv.FormatUint(ledger, 10))
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
