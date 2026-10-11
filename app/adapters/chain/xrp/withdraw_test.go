package xrp

import (
	"context"
	"encoding/asn1"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fakexrpl"
)

type xrpTestAccount struct {
	key     *btcec.PrivateKey
	address string
}

func newXRPTestAccount(t *testing.T) xrpTestAccount {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	address, err := addressing.DeriveXRPAddress(key.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	return xrpTestAccount{key: key, address: address}
}

func altnetAdapter(node *fakexrpl.Server) *Live {
	return NewLive(Config{ChainIDStr: models.ChainTXRP, NativeSymbol: models.NativeXRP, RPCURL: node.URL, IsTestnet: true, Confirmations: 1})
}

func requireDrops(t *testing.T, name string, got *big.Int, want int64) {
	t.Helper()
	if got == nil || got.Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("%s = %v, want %d", name, got, want)
	}
}

func TestPaymentFunds_ReserveComesFromTheValidatedLedger(t *testing.T) {
	t.Parallel()
	node := fakexrpl.New(t)
	source, dest := newXRPTestAccount(t), newXRPTestAccount(t)
	node.Fund(source.address, fakexrpl.Account{Balance: "10000000", Sequence: 5, OwnerCount: 2})
	node.Fund(dest.address, fakexrpl.Account{Balance: "1", Sequence: 1})

	funds, err := altnetAdapter(node).PaymentFunds(context.Background(), source.address, dest.address)
	if err != nil {
		t.Fatal(err)
	}
	requireDrops(t, "balance", funds.Balance, 10_000_000)
	requireDrops(t, "account reserve (1 XRP + 2 × 0.2 XRP)", funds.Reserve, 1_400_000)
	requireDrops(t, "base reserve", funds.BaseReserve, 1_000_000)
	requireDrops(t, "fee", funds.Fee, 12)
	requireDrops(t, "spendable", funds.Spendable(), 10_000_000-1_400_000-12)
	if !funds.DestinationChecked || !funds.DestinationFunded || funds.DestinationRequiresTag {
		t.Fatalf("destination %+v", funds)
	}
	// The source is read on the open ledger, so a payment still pending is counted.
	if got := node.LedgerIndexFor(source.address); got != "current" {
		t.Fatalf("source read on %q", got)
	}
	if got := node.LedgerIndexFor(dest.address); got != "validated" {
		t.Fatalf("destination read on %q", got)
	}

	node.ReserveBaseXRP, node.ReserveIncXRP = "10", "2"
	funds, err = altnetAdapter(node).PaymentFunds(context.Background(), source.address, "")
	if err != nil {
		t.Fatal(err)
	}
	requireDrops(t, "account reserve after a reserve change", funds.Reserve, 14_000_000)
	if funds.DestinationChecked {
		t.Fatal("an empty destination was checked")
	}
}

func TestPaymentFunds_ReserveFallsBackToServerStateThenToTheNetworkValues(t *testing.T) {
	t.Parallel()
	node := fakexrpl.New(t)
	source := newXRPTestAccount(t)
	node.Fund(source.address, fakexrpl.Account{Balance: "5000000", Sequence: 1})
	node.ReserveBaseXRP, node.ReserveIncXRP = "", ""
	node.StateReserveBase, node.StateReserveInc = "2000000", "500000"

	funds, err := altnetAdapter(node).PaymentFunds(context.Background(), source.address, "")
	if err != nil {
		t.Fatal(err)
	}
	requireDrops(t, "server_state base reserve", funds.Reserve, 2_000_000)
	if calls := strings.Join(node.Calls(), ","); !strings.Contains(calls, "server_state") {
		t.Fatalf("server_state not asked: %s", calls)
	}

	node.StateReserveBase = ""
	funds, err = altnetAdapter(node).PaymentFunds(context.Background(), source.address, "")
	if err != nil {
		t.Fatal(err)
	}
	requireDrops(t, "fallback base reserve", funds.BaseReserve, xrpFallbackBaseReserveDrops)
	requireDrops(t, "fallback account reserve", funds.Reserve, xrpFallbackBaseReserveDrops)
}

func TestPaymentFunds_MalformedReserveIsAnErrorNotAFallback(t *testing.T) {
	t.Parallel()
	node := fakexrpl.New(t)
	source := newXRPTestAccount(t)
	node.Fund(source.address, fakexrpl.Account{Balance: "5000000", Sequence: 1})
	node.ReserveBaseXRP = `"-1"`

	if _, err := altnetAdapter(node).PaymentFunds(context.Background(), source.address, ""); err == nil || !strings.Contains(err.Error(), "reserve") {
		t.Fatalf("malformed reserve: %v", err)
	}
}

func TestBuildTransfer_RefusesWhatTheLedgerWouldRefuse(t *testing.T) {
	t.Parallel()
	node := fakexrpl.New(t)
	source, unfunded, funded, tagged := newXRPTestAccount(t), newXRPTestAccount(t), newXRPTestAccount(t), newXRPTestAccount(t)
	node.Fund(source.address, fakexrpl.Account{Balance: "10000000", Sequence: 9, OwnerCount: 1})
	node.Fund(funded.address, fakexrpl.Account{Balance: "20000000", Sequence: 3})
	node.Fund(tagged.address, fakexrpl.Account{Balance: "20000000", Sequence: 3, Flags: fakexrpl.RequireDestTag})
	adapter := altnetAdapter(node)
	build := func(to string, drops int64) (*types.UnsignedTx, error) {
		return adapter.BuildTransfer(context.Background(), types.TransferRequest{From: source.address, To: to, Amount: big.NewInt(drops), Asset: models.NativeXRP})
	}

	if _, err := build(unfunded.address, 999_999); !errors.Is(err, chain.ErrDestinationBelowReserve) {
		t.Fatalf("unfunded destination below the base reserve: %v", err)
	}
	if unsigned, err := build(unfunded.address, 1_000_000); err != nil || unsigned.Metadata["sequence"] != "9" {
		t.Fatalf("unfunded destination at the base reserve: %v", err)
	}
	if _, err := build(funded.address, 1); err != nil {
		t.Fatalf("funded destination: %v", err)
	}
	if _, err := build(tagged.address, 5_000_000); !errors.Is(err, chain.ErrDestinationTagRequired) {
		t.Fatalf("destination requiring a tag: %v", err)
	}
	// 10 XRP − 1.2 XRP reserve (one owned object) − 12 drops fee.
	if _, err := build(funded.address, 8_799_988); err != nil {
		t.Fatalf("the whole spendable balance: %v", err)
	}
	if _, err := build(funded.address, 8_799_989); !errors.Is(err, chain.ErrInsufficientFunds) || !strings.Contains(err.Error(), "reserve") {
		t.Fatalf("one drop into the owner reserve: %v", err)
	}
	if _, err := build(source.address, 1_000_000); !errors.Is(err, chain.ErrInvalidAddress) {
		t.Fatalf("payment to itself: %v", err)
	}
	for _, call := range node.Calls() {
		if call == "submit" {
			t.Fatal("BuildTransfer submitted a payment")
		}
	}
}

func TestFinalizeMPCSignature_MatchesALocalSignatureAndNormalisesS(t *testing.T) {
	t.Parallel()
	node := fakexrpl.New(t)
	source, dest, other := newXRPTestAccount(t), newXRPTestAccount(t), newXRPTestAccount(t)
	node.Fund(source.address, fakexrpl.Account{Balance: "10000000", Sequence: 4})
	node.Fund(dest.address, fakexrpl.Account{Balance: "10000000", Sequence: 1})
	adapter := altnetAdapter(node)
	unsigned, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{From: source.address, To: dest.address, Amount: big.NewInt(2_500_000)})
	if err != nil {
		t.Fatal(err)
	}
	pub := source.key.PubKey().SerializeCompressed()

	digest, err := adapter.MPCSigningDigest(unsigned, pub)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := ecdsa.SignCompact(source.key, digest, true)
	if err != nil {
		t.Fatal(err)
	}
	var s btcec.ModNScalar
	s.SetByteSlice(compact[33:65])
	s.Negate() // a high S, as a threshold ceremony may return
	rs := append([]byte(nil), compact[1:33]...)
	highS := s.Bytes()
	rs = append(rs, highS[:]...)

	signed, err := adapter.FinalizeMPCSignature(unsigned, rs, pub)
	if err != nil {
		t.Fatal(err)
	}
	want, err := adapter.SignTransaction(context.Background(), unsigned, source.key.Serialize())
	if err != nil {
		t.Fatal(err)
	}
	if signed.TxHash != want.TxHash || !bytesEqual(signed.RawBytes, want.RawBytes) {
		t.Fatalf("finalized %s, locally signed %s", signed.TxHash, want.TxHash)
	}
	if err = adapter.VerifySignedTransaction(unsigned, signed, source.address); err != nil {
		t.Fatal(err)
	}
	payment, err := parseXRPPayment(signed.RawBytes)
	if err != nil {
		t.Fatal(err)
	}
	var der struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(payment.TxnSignature, &der); err != nil {
		t.Fatal(err)
	}
	if halfOrder := new(big.Int).Rsh(btcec.S256().N, 1); der.S.Cmp(halfOrder) > 0 {
		t.Fatal("the signed payment carries a high S")
	}

	if _, err := adapter.MPCSigningDigest(unsigned, other.key.PubKey().SerializeCompressed()); err == nil {
		t.Fatal("a key that does not own the account got a digest")
	}
	if _, err := adapter.FinalizeMPCSignature(unsigned, rs, other.key.PubKey().SerializeCompressed()); err == nil {
		t.Fatal("finalized with a key that does not own the account")
	}
	tampered := append([]byte(nil), rs...)
	tampered[63] ^= 1
	if _, err := adapter.FinalizeMPCSignature(unsigned, tampered, pub); err == nil {
		t.Fatal("a signature over another digest was finalized")
	}
	if _, err := adapter.FinalizeMPCSignature(unsigned, rs[:63], pub); err == nil {
		t.Fatal("a 63-byte signature was finalized")
	}
}

func TestGetTransactionBlock_ConfirmsOnlyValidatedSuccess(t *testing.T) {
	t.Parallel()
	node := fakexrpl.New(t)
	node.SetTx("AA01", fakexrpl.Tx{Validated: true, LedgerIndex: 90, Result: "tesSUCCESS", Fee: "12"})
	node.SetTx("AA02", fakexrpl.Tx{Validated: true, LedgerIndex: 91, Result: "tecNO_DST_INSUF_XRP", Fee: "12"})
	node.SetTx("AA03", fakexrpl.Tx{Validated: false, LedgerIndex: 0, Result: "tesSUCCESS", Fee: "12"})
	adapter := altnetAdapter(node)
	ctx := context.Background()

	if ledger, err := adapter.GetTransactionBlock(ctx, "AA01"); err != nil || ledger != 90 {
		t.Fatalf("validated success: ledger %d err %v", ledger, err)
	}
	ledger, err := adapter.GetTransactionBlock(ctx, "AA02")
	if !errors.Is(err, chain.ErrTransactionFailed) || ledger != 0 || !strings.Contains(err.Error(), "tecNO_DST_INSUF_XRP") {
		t.Fatalf("validated tec: ledger %d err %v", ledger, err)
	}
	if ledger, err := adapter.GetTransactionBlock(ctx, "AA03"); err != nil || ledger != 0 {
		t.Fatalf("not validated: ledger %d err %v", ledger, err)
	}
	if ledger, err := adapter.GetTransactionBlock(ctx, "AA04"); err != nil || ledger != 0 {
		t.Fatalf("unknown: ledger %d err %v", ledger, err)
	}
	if tip, err := adapter.GetLatestBlock(ctx); err != nil || tip != 100 {
		t.Fatalf("validated ledger %d err %v", tip, err)
	}
}

func TestTransactionFee_IsTheValidatedFeeInDrops(t *testing.T) {
	t.Parallel()
	node := fakexrpl.New(t)
	node.SetTx("BB01", fakexrpl.Tx{Validated: true, LedgerIndex: 90, Result: "tesSUCCESS", Fee: "12"})
	node.SetTx("BB02", fakexrpl.Tx{Validated: true, LedgerIndex: 91, Result: "tecUNFUNDED_PAYMENT", Fee: "15"})
	node.SetTx("BB03", fakexrpl.Tx{Validated: true, LedgerIndex: 92, Result: "tesSUCCESS", Fee: "20", APIv2: true})
	node.SetTx("BB04", fakexrpl.Tx{Validated: false, Result: "tesSUCCESS", Fee: "12"})
	adapter := altnetAdapter(node)
	ctx := context.Background()

	for hash, want := range map[string]int64{"BB01": 12, "BB02": 15, "BB03": 20} {
		fee, err := adapter.TransactionFee(ctx, hash)
		if err != nil {
			t.Fatalf("%s: %v", hash, err)
		}
		requireDrops(t, hash+" fee", fee, want)
	}
	for _, hash := range []string{"BB04", "BB05"} {
		if _, err := adapter.TransactionFee(ctx, hash); !errors.Is(err, chain.ErrTransactionFeeUnknown) {
			t.Fatalf("%s: %v", hash, err)
		}
	}
}
