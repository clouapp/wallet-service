package bitcoin

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/pkg/types"
)

func TestBitcoinSignP2WPKH(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr, err := addressing.DeriveBtcAddress("bc", priv.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	txid := "1111111111111111111111111111111111111111111111111111111111111111"
	if _, err := chainhash.NewHashFromStr(txid); err != nil {
		t.Fatal(err)
	}
	unsigned := &types.UnsignedTx{
		ChainID: models.ChainBTC,
		Metadata: map[string]interface{}{
			"inputs": []btcInput{{
				TxID:    txid,
				Vout:    0,
				Value:   100000,
				Address: addr,
			}},
			"outputs": []btcOutput{{
				Address: addr,
				Value:   90000,
			}},
		},
	}
	signed, err := signBitcoinP2WPKH(unsigned, priv.Serialize(), &chaincfg.MainNetParams)
	if err != nil {
		t.Fatal(err)
	}
	msg := &wire.MsgTx{}
	if err := msg.Deserialize(bytes.NewReader(signed.RawBytes)); err != nil {
		t.Fatal(err)
	}
	if len(msg.TxIn) != 1 || len(msg.TxIn[0].Witness) != 2 {
		t.Fatalf("witness %d", len(msg.TxIn[0].Witness))
	}
}

func TestBitcoinBuildTransferREST(t *testing.T) {
	const txid = "2222222222222222222222222222222222222222222222222222222222222222"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/address/bc1qexample/utxo" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `[{"txid":"`+txid+`","vout":0,"value":200000,"status":{"confirmed":true}}]`)
	}))
	defer srv.Close()

	live := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainBTC, NativeSymbol: models.NativeBTC, RPCURL: srv.URL})
	live.restAPI = true
	unsigned, err := live.BuildTransfer(context.Background(), types.TransferRequest{
		From:   "bc1qexample",
		To:     "bc1qsecond",
		Amount: big.NewInt(100000),
	})
	if err != nil {
		t.Fatal(err)
	}
	outputs, ok := unsigned.Metadata["outputs"].([]btcOutput)
	if !ok {
		t.Fatalf("%+v", unsigned.Metadata)
	}
	var sum int64
	for _, out := range outputs {
		sum += out.Value
	}
	const fee = int64(140 * 10)
	if sum+fee != 200000 {
		t.Fatalf("outputs %d fee %d", sum, fee)
	}
}

func TestBitcoinBroadcastREST(t *testing.T) {
	raw := []byte{0x01, 0x02}
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/tx" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_, _ = io.WriteString(w, "abc123\n")
	}))
	defer srv.Close()

	live := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainBTC, RPCURL: srv.URL})
	live.restAPI = true
	got, err := live.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: raw})
	if err != nil {
		t.Fatal(err)
	}
	if got != "abc123" || gotBody != hex.EncodeToString(raw) {
		t.Fatalf("sig %s body %s", got, gotBody)
	}
}

func TestBitcoinBuildSweep(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/utxo") {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `[{"txid":"3333333333333333333333333333333333333333333333333333333333333333","vout":1,"value":200000,"status":{"confirmed":true}}]`)
	}))
	defer srv.Close()
	live := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainBTC, RPCURL: srv.URL})
	live.restAPI = true
	txs, err := live.BuildSweep(context.Background(), types.SweepRequest{
		From:   "bc1qexample",
		To:     "bc1qsecond",
		Amount: big.NewInt(100000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 1 {
		t.Fatalf("len %d", len(txs))
	}
}
