package bitcoin

import (
	"bytes"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/wire"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/pkg/types"
)

const verifyFundingTxID = "3333333333333333333333333333333333333333333333333333333333333333"

type p2wpkhFixture struct {
	key      *btcec.PrivateKey
	address  string
	unsigned *types.UnsignedTx
}

func newP2WPKHFixture(t *testing.T) *p2wpkhFixture {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	address, err := addressing.DeriveBtcAddress(addressing.BtcHRPTestnet, key.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	return &p2wpkhFixture{key: key, address: address, unsigned: &types.UnsignedTx{
		ChainID: models.ChainBTC,
		Metadata: map[string]interface{}{
			"inputs":  []btcInput{{TxID: verifyFundingTxID, Vout: 1, Value: 20_000, Address: address}},
			"outputs": []btcOutput{{Address: address, Value: 10_000}, {Address: address, Value: 9_000}},
			"testnet": true,
		},
	}}
}

func (f *p2wpkhFixture) sign(t *testing.T, key *btcec.PrivateKey) *types.SignedTx {
	t.Helper()
	return signBitcoinP2WPKHForTest(t, f.unsigned, key.Serialize())
}

func reserialize(t *testing.T, signed *types.SignedTx, mutate func(*wire.MsgTx)) *types.SignedTx {
	t.Helper()
	var msg wire.MsgTx
	if err := msg.Deserialize(bytes.NewReader(signed.RawBytes)); err != nil {
		t.Fatal(err)
	}
	mutate(&msg)
	var buf bytes.Buffer
	if err := msg.Serialize(&buf); err != nil {
		t.Fatal(err)
	}
	return &types.SignedTx{ChainID: signed.ChainID, RawBytes: buf.Bytes()}
}

func TestVerify_SignedP2WPKH_AcceptsTheOwnersSignature(t *testing.T) {
	fixture := newP2WPKHFixture(t)
	if err := verifySignedP2WPKH(fixture.unsigned, fixture.sign(t, fixture.key), fixture.address); err != nil {
		t.Fatal(err)
	}
}

func TestVerify_SignedP2WPKH_RejectsAnotherKeysSignature(t *testing.T) {
	fixture := newP2WPKHFixture(t)
	other, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	err = verifySignedP2WPKH(fixture.unsigned, fixture.sign(t, other), fixture.address)
	if err == nil || !strings.Contains(err.Error(), "does not own") {
		t.Fatalf("want ownership error, got %v", err)
	}
}

func TestVerify_SignedP2WPKH_RejectsTamperedTransactions(t *testing.T) {
	fixture := newP2WPKHFixture(t)
	signed := fixture.sign(t, fixture.key)
	cases := map[string]func(*wire.MsgTx){
		"output value":   func(m *wire.MsgTx) { m.TxOut[0].Value++ },
		"extra output":   func(m *wire.MsgTx) { m.AddTxOut(wire.NewTxOut(1, m.TxOut[0].PkScript)) },
		"input sequence": func(m *wire.MsgTx) { m.TxIn[0].Sequence-- },
		"lock time":      func(m *wire.MsgTx) { m.LockTime = 1 },
		"signature byte": func(m *wire.MsgTx) { m.TxIn[0].Witness[0][10] ^= 0x01 },
		"sighash type":   func(m *wire.MsgTx) { w := m.TxIn[0].Witness[0]; w[len(w)-1] = 0x83 },
		"missing witness": func(m *wire.MsgTx) {
			m.TxIn[0].Witness = m.TxIn[0].Witness[:1]
		},
	}
	for name, mutate := range cases {
		if err := verifySignedP2WPKH(fixture.unsigned, reserialize(t, signed, mutate), fixture.address); err == nil {
			t.Errorf("%s: tampered transaction was accepted", name)
		}
	}
}

func TestVerify_SignedP2WPKH_RejectsAnotherSourceAddress(t *testing.T) {
	fixture := newP2WPKHFixture(t)
	other := newP2WPKHFixture(t)
	err := verifySignedP2WPKH(fixture.unsigned, fixture.sign(t, fixture.key), other.address)
	if err == nil || !strings.Contains(err.Error(), "expected "+other.address) {
		t.Fatalf("want source mismatch, got %v", err)
	}
}

func TestVerify_SignedP2WPKH_RejectsAWrongReportedTxid(t *testing.T) {
	fixture := newP2WPKHFixture(t)
	signed := fixture.sign(t, fixture.key)
	signed.TxHash = verifyFundingTxID
	if err := verifySignedP2WPKH(fixture.unsigned, signed, fixture.address); err == nil {
		t.Fatal("a txid that is not the transaction's hash must be refused")
	}
}
