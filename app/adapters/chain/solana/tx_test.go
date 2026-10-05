package solana

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

func TestSolanaSignNative(t *testing.T) {
	seed := bytes.Repeat([]byte{0x07}, 32)
	priv := ed25519.NewKeyFromSeed(seed)
	from := solana.PublicKeyFromBytes(priv.Public().(ed25519.PublicKey))
	to := solana.MustPublicKeyFromBase58("7EcDhSYGxXyscszYEp35KHN8vvw3svAuLKTzXwCFLtV")
	var blockhash solana.Hash
	blockhash[0] = 1
	raw, err := buildSolanaNativeTx(from, to, 1000, blockhash.String())
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signSolanaTx(&types.UnsignedTx{ChainID: models.ChainSOL, RawBytes: raw}, seed)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := solana.TransactionFromBytes(signed.RawBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.VerifySignatures(); err != nil {
		t.Fatal(err)
	}
}

func TestSolanaBroadcast(t *testing.T) {
	var method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		method = req.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"sig123"}`)
	}))
	defer srv.Close()

	live := NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, RPCURL: srv.URL})
	got, err := live.BroadcastTransaction(context.Background(), &types.SignedTx{
		ChainID:  models.ChainSOL,
		RawBytes: []byte{0x01},
	})
	if err != nil {
		t.Fatal(err)
	}
	if method != "sendTransaction" || got != "sig123" {
		t.Fatalf("method %s sig %s", method, got)
	}
}

func TestSolanaSignSPL(t *testing.T) {
	seed := bytes.Repeat([]byte{0x07}, 32)
	priv := ed25519.NewKeyFromSeed(seed)
	owner := solana.PublicKeyFromBytes(priv.Public().(ed25519.PublicKey))
	dest := solana.MustPublicKeyFromBase58(solanaBalanceOwner)
	mint := solana.MustPublicKeyFromBase58(models.USDCMintSOL)
	var blockhash solana.Hash
	blockhash[0] = 1
	raw, err := buildSolanaSPLTx(owner, dest, mint, 1_500_000, blockhash.String(), false)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signSolanaTx(&types.UnsignedTx{ChainID: models.ChainSOL, RawBytes: raw}, seed)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := solana.TransactionFromBytes(signed.RawBytes)
	if err != nil {
		t.Fatal(err)
	}
	var amount uint64
	for _, ix := range tx.Message.Instructions {
		prog := tx.Message.AccountKeys[ix.ProgramIDIndex]
		if !prog.Equals(solana.TokenProgramID) || len(ix.Data) < 8 {
			continue
		}
		amount = binary.LittleEndian.Uint64(ix.Data[len(ix.Data)-8:])
	}
	if amount != 1_500_000 {
		t.Fatalf("spl amount %d", amount)
	}
}

func TestSolanaSweepNative(t *testing.T) {
	live, from, to := newSweepSolana(t)
	txs, err := live.BuildSweep(context.Background(), types.SweepRequest{
		From:          from,
		To:            to,
		NativeBalance: big.NewInt(6000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 1 {
		t.Fatalf("len %d", len(txs))
	}
	if lamports := messageLamports(t, txs[0].RawBytes); lamports != 1000 {
		t.Fatalf("lamports %d", lamports)
	}

	_, err = live.BuildSweep(context.Background(), types.SweepRequest{
		From:          from,
		To:            to,
		NativeBalance: big.NewInt(4000),
	})
	if err == nil || !strings.Contains(err.Error(), "insufficient native for fee") {
		t.Fatalf("err %v", err)
	}
}

func TestSolanaSweepSPL(t *testing.T) {
	live, from, to := newSweepSolana(t)
	token := &types.Token{Symbol: models.SymbolUSDC, Contract: models.USDCMintSOL, Decimals: 6, ChainID: models.ChainSOL}
	_, err := live.BuildSweep(context.Background(), types.SweepRequest{
		From:          from,
		To:            to,
		NativeBalance: big.NewInt(4999),
		Amount:        big.NewInt(10),
		Token:         token,
	})
	if err == nil || !strings.Contains(err.Error(), "insufficient native for fee") {
		t.Fatalf("err %v", err)
	}
	txs, err := live.BuildSweep(context.Background(), types.SweepRequest{
		From:          from,
		To:            to,
		NativeBalance: big.NewInt(5000),
		Amount:        big.NewInt(10),
		Token:         token,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 1 {
		t.Fatalf("len %d", len(txs))
	}
}

func TestSolanaBuildTransferCreatesMissingATA(t *testing.T) {
	var sawAccount bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "getAccountInfo" {
			sawAccount = true
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"could not find account"}}`)
			return
		}
		hash := solana.Hash{}
		hash[0] = 1
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"value":{"blockhash":"`+hash.String()+`"}}}`)
	}))
	defer srv.Close()

	seed := bytes.Repeat([]byte{0x07}, 32)
	priv := ed25519.NewKeyFromSeed(seed)
	from := solana.PublicKeyFromBytes(priv.Public().(ed25519.PublicKey)).String()
	live := NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, RPCURL: srv.URL})
	unsigned, err := live.BuildTransfer(context.Background(), types.TransferRequest{
		From:   from,
		To:     solanaBalanceOwner,
		Amount: big.NewInt(1_500_000),
		Token:  &types.Token{Symbol: models.SymbolUSDC, Contract: models.USDCMintSOL, Decimals: 6},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawAccount {
		t.Fatal("expected getAccountInfo")
	}
	if unsigned.Metadata["dest_ata_exists"] != false {
		t.Fatalf("metadata %+v", unsigned.Metadata)
	}
}

func newSweepSolana(t *testing.T) (*SolanaLive, string, string) {
	t.Helper()
	hash := solana.Hash{}
	hash[0] = 1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "getAccountInfo" {
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"value":{"lamports":1}}}`)
			return
		}
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"value":{"blockhash":"`+hash.String()+`"}}}`)
	}))
	t.Cleanup(srv.Close)
	seed := bytes.Repeat([]byte{0x07}, 32)
	priv := ed25519.NewKeyFromSeed(seed)
	from := solana.PublicKeyFromBytes(priv.Public().(ed25519.PublicKey)).String()
	return NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, RPCURL: srv.URL}), from, solanaBalanceOwner
}

func messageLamports(t *testing.T, raw []byte) uint64 {
	t.Helper()
	msg := &solana.Message{}
	if err := msg.UnmarshalWithDecoder(bin.NewBinDecoder(raw)); err != nil {
		t.Fatal(err)
	}
	if len(msg.Instructions) == 0 || len(msg.Instructions[0].Data) < 8 {
		t.Fatal("missing instruction")
	}
	data := msg.Instructions[0].Data
	return binary.LittleEndian.Uint64(data[len(data)-8:])
}
