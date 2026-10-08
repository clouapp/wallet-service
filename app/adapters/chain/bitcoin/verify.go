package bitcoin

import (
	"bytes"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"

	"github.com/macrowallets/waas/pkg/types"
)

// VerifySignedTransaction checks a signed P2WPKH transaction before broadcast: it
// must be the transaction that was built (same inputs, outputs, version and lock
// time), every input must spend from `from`, and every witness must carry a public
// key hashing to that input's witness program with a low-S SIGHASH_ALL signature
// over the input's BIP-143 digest. This is the check a node runs as OP_EQUALVERIFY
// and OP_CHECKSIG, so a key that does not own the input never reaches the network.
func (a *BitcoinLive) VerifySignedTransaction(unsigned *types.UnsignedTx, signed *types.SignedTx, from string) error {
	if err := a.requireBuiltOnThisNetwork(unsigned); err != nil {
		return fmt.Errorf("btc verify: %w", err)
	}
	return verifySignedP2WPKH(unsigned, signed, from, a.network.params)
}

func verifySignedP2WPKH(unsigned *types.UnsignedTx, signed *types.SignedTx, from string, net *chaincfg.Params) error {
	if unsigned == nil || signed == nil || len(signed.RawBytes) == 0 {
		return fmt.Errorf("btc verify: unsigned and signed transactions are required")
	}
	if net == nil {
		return fmt.Errorf("btc verify: network parameters are required")
	}
	inputs := inputsFrom(unsigned)
	if len(inputs) == 0 {
		return fmt.Errorf("btc verify: transaction has no inputs")
	}
	built, err := unsignedToMsgTx(unsigned, net)
	if err != nil {
		return fmt.Errorf("btc verify: rebuild transaction: %w", err)
	}
	var decoded wire.MsgTx
	if err := decoded.Deserialize(bytes.NewReader(signed.RawBytes)); err != nil {
		return fmt.Errorf("btc verify: decode signed transaction: %w", err)
	}
	if err := sameUnsignedBody(built, &decoded); err != nil {
		return fmt.Errorf("btc verify: %w", err)
	}
	if signed.TxHash != "" && signed.TxHash != decoded.TxHash().String() {
		return fmt.Errorf("btc verify: reported txid %s, transaction hashes to %s", signed.TxHash, decoded.TxHash().String())
	}
	for i, in := range inputs {
		if from != "" && in.Address != from {
			return fmt.Errorf("btc verify: input %d spends from %s, expected %s", i, in.Address, from)
		}
		if err := verifyP2WPKHInput(&decoded, i, in, net); err != nil {
			return fmt.Errorf("btc verify: input %d: %w", i, err)
		}
	}
	return nil
}

func sameUnsignedBody(built, decoded *wire.MsgTx) error {
	if built.Version != decoded.Version || built.LockTime != decoded.LockTime {
		return fmt.Errorf("signed transaction version or lock time differs from the built one")
	}
	if len(built.TxIn) != len(decoded.TxIn) || len(built.TxOut) != len(decoded.TxOut) {
		return fmt.Errorf("signed transaction has a different number of inputs or outputs")
	}
	for i := range built.TxIn {
		if built.TxIn[i].PreviousOutPoint != decoded.TxIn[i].PreviousOutPoint || built.TxIn[i].Sequence != decoded.TxIn[i].Sequence {
			return fmt.Errorf("signed input %d differs from the built one", i)
		}
		if len(decoded.TxIn[i].SignatureScript) != 0 {
			return fmt.Errorf("signed input %d has a script sig; P2WPKH spends must not", i)
		}
	}
	for i := range built.TxOut {
		if built.TxOut[i].Value != decoded.TxOut[i].Value || !bytes.Equal(built.TxOut[i].PkScript, decoded.TxOut[i].PkScript) {
			return fmt.Errorf("signed output %d differs from the built one", i)
		}
	}
	return nil
}

func verifyP2WPKHInput(msg *wire.MsgTx, index int, in btcInput, net *chaincfg.Params) error {
	program, err := witnessProgram(in.Address, net)
	if err != nil {
		return err
	}
	witness := msg.TxIn[index].Witness
	if len(witness) != 2 {
		return fmt.Errorf("witness has %d items, want signature and public key", len(witness))
	}
	signatureWithHashType, publicKeyBytes := witness[0], witness[1]
	if !bytes.Equal(btcutil.Hash160(publicKeyBytes), program) {
		return fmt.Errorf("witness public key does not own %s", in.Address)
	}
	publicKey, err := btcec.ParsePubKey(publicKeyBytes)
	if err != nil {
		return fmt.Errorf("parse witness public key: %w", err)
	}
	if len(signatureWithHashType) < 2 || signatureWithHashType[len(signatureWithHashType)-1] != btcSigHashAll {
		return fmt.Errorf("signature is not SIGHASH_ALL")
	}
	der := signatureWithHashType[:len(signatureWithHashType)-1]
	signature, err := ecdsa.ParseDERSignature(der)
	if err != nil {
		return fmt.Errorf("parse signature: %w", err)
	}
	if !bytes.Equal(signature.Serialize(), der) {
		return fmt.Errorf("signature is not canonical low-S DER, which nodes do not relay")
	}
	digest, err := bip143Sighash(msg, index, in.Value, p2wpkhScriptCode(program))
	if err != nil {
		return err
	}
	if !signature.Verify(digest, publicKey) {
		return fmt.Errorf("signature does not verify for %s", in.Address)
	}
	return nil
}
