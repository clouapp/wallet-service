package xrp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/pkg/types"
)

func TestPaymentCodecRoundTripAndLowS(t *testing.T) {
	t.Parallel()

	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	from, err := addressing.DeriveXRPAddress(key.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	to, err := addressing.DeriveXRPAddress(compressedPub(bytesRepeat(2)))
	if err != nil {
		t.Fatal(err)
	}
	fromID, _ := addressing.DecodeXRPClassicAddress(from)
	toID, _ := addressing.DecodeXRPClassicAddress(to)
	payment := xrpPayment{
		Account: fromID, Destination: toID, Amount: 1_000_000, Fee: 10,
		Sequence: 3, LastLedgerSequence: 40, SigningPubKey: key.PubKey().SerializeCompressed(),
	}
	preimage, err := payment.serialize(true, false)
	if err != nil {
		t.Fatal(err)
	}
	sig := ecdsa.Sign(key, xrpSHA512Half(xrpSignPrefix, preimage))
	payment.TxnSignature = sig.Serialize()
	blob, err := payment.serialize(true, true)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseXRPPayment(blob)
	if err != nil || parsed.Amount != payment.Amount || parsed.Fee != 10 || parsed.Sequence != 3 {
		t.Fatalf("parsed %+v err %v", parsed, err)
	}
	if !bytesEqual(parsed.Account, fromID) || !bytesEqual(parsed.TxnSignature, payment.TxnSignature) {
		t.Fatal("parsed payment does not match")
	}
	decoded, err := ecdsa.ParseDERSignature(parsed.TxnSignature)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.Verify(xrpSHA512Half(xrpSignPrefix, preimage), key.PubKey()) {
		t.Fatal("signature did not verify")
	}
}

func TestBuildSignBroadcastOneAltnetPayment(t *testing.T) {
	t.Parallel()

	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	priv := key.Serialize()
	from, err := addressing.DeriveXRPAddress(key.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	other, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	to, err := addressing.DeriveXRPAddress(other.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}

	var submitted string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		switch {
		case strings.Contains(text, "server_info"):
			_, _ = io.WriteString(w, `{"result":{"status":"success","info":{"network_id":1,"validated_ledger":{"reserve_base_xrp":1,"seq":100}}}}`)
		case strings.Contains(text, `"method":"fee"`):
			_, _ = io.WriteString(w, `{"result":{"status":"success","drops":{"open_ledger_fee":"10","minimum_fee":"10"}}}`)
		case strings.Contains(text, "account_info"):
			if strings.Contains(text, from) {
				_, _ = io.WriteString(w, `{"result":{"status":"success","account_data":{"Balance":"10000000","Sequence":7}}}`)
				return
			}
			_, _ = io.WriteString(w, `{"result":{"status":"error","error":"actNotFound"}}`)
		case strings.Contains(text, "submit"):
			submitted = text
			var req struct {
				Params []struct {
					TxBlob string `json:"tx_blob"`
				} `json:"params"`
			}
			_ = json.Unmarshal(body, &req)
			raw, _ := hex.DecodeString(req.Params[0].TxBlob)
			sum := strings.ToUpper(hex.EncodeToString(xrpSHA512Half(xrpHashPrefix, raw)))
			_, _ = fmt.Fprintf(w, `{"result":{"status":"success","engine_result":"tesSUCCESS","tx_json":{"hash":"%s"}}}`, sum)
		default:
			t.Errorf("unexpected rpc %s", text)
			http.Error(w, "no", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	adapter := NewLive(Config{
		ChainIDStr: models.ChainTXRP, NativeSymbol: models.NativeXRP, RPCURL: server.URL, IsTestnet: true,
	})
	unsigned, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{
		From: from, To: to, Amount: big.NewInt(1_000_000), Asset: models.NativeXRP,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unsigned.TransferAmount.String() != "1000000" || unsigned.Metadata["sequence"] != "7" || unsigned.Metadata["fee_drops"] != "10" {
		t.Fatalf("unsigned %+v", unsigned.Metadata)
	}
	if _, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{
		From: from, To: to, Amount: big.NewInt(9_000_000), Asset: models.NativeXRP,
	}); err == nil || !strings.Contains(err.Error(), "reserve") {
		t.Fatalf("reserve: %v", err)
	}
	signed, err := adapter.SignTransaction(context.Background(), unsigned, priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.VerifySignedTransaction(unsigned, signed, from); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.SignTransaction(context.Background(), unsigned, other.Serialize()); err == nil {
		t.Fatal("a different key signed the payment")
	}
	hash, err := adapter.BroadcastTransaction(context.Background(), signed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(hash, signed.TxHash) || !strings.Contains(submitted, strings.ToLower(hex.EncodeToString(signed.RawBytes))) {
		t.Fatalf("broadcast %s submitted %s", hash, submitted)
	}

	mainnet := NewLive(Config{ChainIDStr: models.ChainXRP, NativeSymbol: models.NativeXRP, RPCURL: "https://s1.ripple.com:51234", IsTestnet: true})
	if _, err := mainnet.BuildTransfer(context.Background(), types.TransferRequest{From: from, To: to, Amount: big.NewInt(1)}); err == nil || !strings.Contains(err.Error(), "not the altnet") {
		t.Fatalf("mainnet: %v", err)
	}
}

func TestUnsignedEncodingMatchesXRPLJS(t *testing.T) {
	t.Parallel()

	pub, err := hex.DecodeString("03D0E8E8DE13F61B31BC31C42CBA77E435E02657174BAB5FDE5E8F11153C31C42E")
	if err != nil {
		t.Fatal(err)
	}
	from, err := addressing.DeriveXRPAddress(pub)
	if err != nil || from != "rhS79rpngkE7FZQnbr2h7QMESLJr3nSm4b" {
		t.Fatalf("address %s err %v", from, err)
	}
	fromID, err := addressing.DecodeXRPClassicAddress(from)
	if err != nil {
		t.Fatal(err)
	}
	destID, err := addressing.DecodeXRPClassicAddress("rPT1Sjq2YGrBMTttX4GZHjKu9dyfzbpAYe")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := (xrpPayment{
		Account: fromID, Destination: destID, Amount: 1_000_000, Fee: 12,
		Sequence: 1, LastLedgerSequence: 20, SigningPubKey: pub,
	}).serialize(true, false)
	if err != nil {
		t.Fatal(err)
	}
	const want = "1200002400000001201b000000146140000000000f424068400000000000000c732103d0e8e8de13f61b31bc31c42cba77e435e02657174bab5fde5e8f11153c31c42e811425b96e14c98ad7970564b551e6d5150154a43a228314f667b0ca50cc7709a220b0561b85e53a48461fa8"
	if hex.EncodeToString(blob) != want {
		t.Fatalf("blob %x", blob)
	}
}

func bytesRepeat(b byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = b
	}
	return out
}
