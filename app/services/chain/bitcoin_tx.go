package chain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	btcFeeVBytes            = 140
	btcDefaultFeeRate       = 10
	btcDustSats       int64 = 546
)

const btcSigHashAll byte = 0x01

type btcInput struct {
	TxID    string
	Vout    uint32
	Value   int64
	Address string
}

type btcOutput struct {
	Address string
	Value   int64
}

func signBitcoinP2WPKH(unsigned *types.UnsignedTx, privateKey []byte, net *chaincfg.Params) (*types.SignedTx, error) {
	if len(privateKey) != 32 {
		return nil, fmt.Errorf("btc private key must be 32 bytes")
	}
	if net == nil {
		net = netParams(unsigned)
	}
	priv, _ := btcec.PrivKeyFromBytes(privateKey)
	msg, err := unsignedToMsgTx(unsigned, net)
	if err != nil {
		return nil, err
	}
	for i, in := range inputsFrom(unsigned) {
		program, err := witnessProgram(in.Address, net)
		if err != nil {
			return nil, err
		}
		witness, err := p2wpkhWitness(msg, i, in.Value, program, priv)
		if err != nil {
			return nil, err
		}
		msg.TxIn[i].Witness = witness
	}
	var buf bytes.Buffer
	if err := msg.Serialize(&buf); err != nil {
		return nil, err
	}
	return &types.SignedTx{ChainID: unsigned.ChainID, RawBytes: buf.Bytes(), TxHash: msg.TxHash().String()}, nil
}

func inputsFrom(unsigned *types.UnsignedTx) []btcInput {
	if unsigned == nil || unsigned.Metadata == nil {
		return nil
	}
	raw, _ := unsigned.Metadata["inputs"].([]btcInput)
	return raw
}

func outputsFrom(unsigned *types.UnsignedTx) ([]btcOutput, error) {
	if unsigned == nil || unsigned.Metadata == nil {
		return nil, fmt.Errorf("btc outputs missing")
	}
	raw, ok := unsigned.Metadata["outputs"].([]btcOutput)
	if !ok {
		return nil, fmt.Errorf("btc outputs missing")
	}
	return raw, nil
}

func unsignedToMsgTx(unsigned *types.UnsignedTx, net *chaincfg.Params) (*wire.MsgTx, error) {
	if net == nil {
		net = netParams(unsigned)
	}
	msg := wire.NewMsgTx(wire.TxVersion)
	for _, in := range inputsFrom(unsigned) {
		hash, err := chainhash.NewHashFromStr(in.TxID)
		if err != nil {
			return nil, err
		}
		op := wire.OutPoint{Hash: *hash, Index: in.Vout}
		msg.AddTxIn(wire.NewTxIn(&op, nil, nil))
	}
	outputs, err := outputsFrom(unsigned)
	if err != nil {
		return nil, err
	}
	for _, out := range outputs {
		program, err := witnessProgram(out.Address, net)
		if err != nil {
			return nil, err
		}
		msg.AddTxOut(wire.NewTxOut(out.Value, p2wpkhPkScript(program)))
	}
	return msg, nil
}

func netParams(unsigned *types.UnsignedTx) *chaincfg.Params {
	if unsigned != nil && unsigned.Metadata != nil {
		if testnet, ok := unsigned.Metadata["testnet"].(bool); ok && testnet {
			return &chaincfg.TestNet3Params
		}
	}
	return &chaincfg.MainNetParams
}

func witnessProgram(address string, net *chaincfg.Params) ([]byte, error) {
	addr, err := btcutil.DecodeAddress(address, net)
	if err != nil {
		return nil, err
	}
	wpkh, ok := addr.(*btcutil.AddressWitnessPubKeyHash)
	if !ok {
		return nil, fmt.Errorf("btc address %s is not p2wpkh", address)
	}
	return wpkh.WitnessProgram(), nil
}

func p2wpkhPkScript(program []byte) []byte {
	script := make([]byte, 0, 22)
	script = append(script, 0x00, 0x14)
	return append(script, program...)
}

func p2wpkhScriptCode(program []byte) []byte {
	script := make([]byte, 0, 25)
	script = append(script, 0x76, 0xa9, 0x14)
	script = append(script, program...)
	return append(script, 0x88, 0xac)
}

func p2wpkhWitness(msg *wire.MsgTx, idx int, value int64, program []byte, priv *btcec.PrivateKey) (wire.TxWitness, error) {
	sighash, err := bip143Sighash(msg, idx, value, p2wpkhScriptCode(program))
	if err != nil {
		return nil, err
	}
	sig := ecdsa.Sign(priv, sighash)
	der := append(sig.Serialize(), btcSigHashAll)
	return wire.TxWitness{der, priv.PubKey().SerializeCompressed()}, nil
}

func bip143Sighash(msg *wire.MsgTx, idx int, value int64, scriptCode []byte) ([]byte, error) {
	if idx < 0 || idx >= len(msg.TxIn) {
		return nil, fmt.Errorf("btc input index %d", idx)
	}
	var prevouts bytes.Buffer
	var sequences bytes.Buffer
	for _, in := range msg.TxIn {
		if _, err := prevouts.Write(in.PreviousOutPoint.Hash[:]); err != nil {
			return nil, err
		}
		if err := binary.Write(&prevouts, binary.LittleEndian, in.PreviousOutPoint.Index); err != nil {
			return nil, err
		}
		if err := binary.Write(&sequences, binary.LittleEndian, in.Sequence); err != nil {
			return nil, err
		}
	}
	var outputs bytes.Buffer
	for _, out := range msg.TxOut {
		if err := binary.Write(&outputs, binary.LittleEndian, out.Value); err != nil {
			return nil, err
		}
		if err := writeVarBytes(&outputs, out.PkScript); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, uint32(msg.Version)); err != nil {
		return nil, err
	}
	buf.Write(hash256(prevouts.Bytes()))
	buf.Write(hash256(sequences.Bytes()))
	in := msg.TxIn[idx]
	if _, err := buf.Write(in.PreviousOutPoint.Hash[:]); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, in.PreviousOutPoint.Index); err != nil {
		return nil, err
	}
	if err := writeVarBytes(&buf, scriptCode); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, value); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, in.Sequence); err != nil {
		return nil, err
	}
	buf.Write(hash256(outputs.Bytes()))
	if err := binary.Write(&buf, binary.LittleEndian, msg.LockTime); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint32(btcSigHashAll)); err != nil {
		return nil, err
	}
	return hash256(buf.Bytes()), nil
}

func writeVarBytes(buf *bytes.Buffer, b []byte) error {
	if len(b) >= 0xfd {
		return fmt.Errorf("btc script longer than 252 bytes")
	}
	if err := buf.WriteByte(byte(len(b))); err != nil {
		return err
	}
	_, err := buf.Write(b)
	return err
}

func (a *BitcoinLive) buildBitcoinTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	if req.Amount == nil || !req.Amount.IsInt64() || req.Amount.Sign() <= 0 {
		return nil, fmt.Errorf("amount is required")
	}
	return a.assembleBitcoinTx(ctx, req.From, req.To, req.Amount.Int64())
}

// buildBitcoinSweep spends every confirmed UTXO of req.From. Without an amount it
// sends all of it after the fee; with one it sends that amount and returns change
// of at least dust to req.From (smaller leftovers go to the fee).
func (a *BitcoinLive) buildBitcoinSweep(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
	if err := requireBTCEndpoints(req.From, req.To); err != nil {
		return nil, err
	}
	if req.Amount != nil && (!req.Amount.IsInt64() || req.Amount.Sign() <= 0) {
		return nil, fmt.Errorf("amount is required")
	}
	utxos, err := a.listConfirmedUTXOs(ctx, req.From)
	if err != nil {
		return nil, err
	}
	policy := a.feePolicy(ctx)
	inputs := spendableBTCInputs(utxos)

	amount := maxSendableSats(inputs, policy)
	if req.Amount != nil {
		amount = req.Amount.Int64()
	}
	if amount < btcDustSats {
		return nil, fmt.Errorf("insufficient funds: %d confirmed sats leave %d sats after fees, below the %d sat dust limit",
			sumBTCInputs(inputs), amount, btcDustSats)
	}
	spend, ok := spendFromInputs(inputs, amount, policy)
	if !ok {
		return nil, fmt.Errorf("insufficient funds: %d confirmed sats in %d utxos cannot pay %d sats plus fee %d",
			sumBTCInputs(inputs), len(inputs), amount, policy.fee(max(len(inputs), btcTypicalInputs), btcOutputsPaymentOnly))
	}
	return []types.UnsignedTx{*a.unsignedBitcoinTx(req.From, req.To, amount, spend)}, nil
}

func (a *BitcoinLive) assembleBitcoinTx(ctx context.Context, from, to string, amount int64) (*types.UnsignedTx, error) {
	if err := requireBTCEndpoints(from, to); err != nil {
		return nil, err
	}
	utxos, err := a.listConfirmedUTXOs(ctx, from)
	if err != nil {
		return nil, err
	}
	spend, err := selectBTCSpend(utxos, amount, a.feePolicy(ctx))
	if err != nil {
		return nil, err
	}
	return a.unsignedBitcoinTx(from, to, amount, spend), nil
}

func (a *BitcoinLive) unsignedBitcoinTx(from, to string, amount int64, spend btcSpend) *types.UnsignedTx {
	return &types.UnsignedTx{
		ChainID: a.cfg.ChainIDStr,
		Metadata: map[string]interface{}{
			"inputs":  spend.inputs,
			"outputs": spend.outputs(from, to, amount),
			"fee":     spend.fee,
			"testnet": a.cfg.IsTestnet,
		},
		TransferAmount: big.NewInt(amount),
	}
}

func requireBTCEndpoints(from, to string) error {
	if strings.TrimSpace(from) == "" {
		return fmt.Errorf("btc transfer: from address is required")
	}
	if strings.TrimSpace(to) == "" {
		return fmt.Errorf("btc transfer: to address is required")
	}
	return nil
}

// btcPaymentOutputs pays amount to to, plus change back to from when change > 0;
// callers only pass change of at least dust.
func btcPaymentOutputs(from, to string, amount, change int64) []btcOutput {
	if change > 0 {
		return []btcOutput{{Address: to, Value: amount}, {Address: from, Value: change}}
	}
	return []btcOutput{{Address: to, Value: amount}}
}

func (a *BitcoinLive) listConfirmedUTXOs(ctx context.Context, address string) ([]btcInput, error) {
	if a.restAPI {
		return a.listUTXOsREST(ctx, address)
	}
	return a.listUTXOsRPC(ctx, address)
}

func (a *BitcoinLive) listUTXOsREST(ctx context.Context, address string) ([]btcInput, error) {
	if strings.TrimSpace(address) == "" || strings.ContainsAny(address, "/?#") {
		return nil, fmt.Errorf("btc utxo: invalid address %q", address)
	}
	body, err := a.esploraGet(ctx, "/address/"+address+"/utxo")
	if err != nil {
		return nil, fmt.Errorf("btc utxo: %w", err)
	}
	var raw []struct {
		TxID   string `json:"txid"`
		Vout   uint32 `json:"vout"`
		Value  int64  `json:"value"`
		Status struct {
			Confirmed bool `json:"confirmed"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]btcInput, 0, len(raw))
	for _, utxo := range raw {
		if !utxo.Status.Confirmed {
			continue
		}
		out = append(out, btcInput{TxID: utxo.TxID, Vout: utxo.Vout, Value: utxo.Value, Address: address})
	}
	return out, nil
}

func (a *BitcoinLive) listUTXOsRPC(ctx context.Context, address string) ([]btcInput, error) {
	var raw []struct {
		TxID   string          `json:"txid"`
		Vout   uint32          `json:"vout"`
		Amount decimal.Decimal `json:"amount"`
	}
	if err := a.rpc.Call(ctx, "listunspent", &raw, 1, 9999999, []string{address}); err != nil {
		return nil, err
	}
	out := make([]btcInput, 0, len(raw))
	for _, utxo := range raw {
		sats, err := btcToSats(utxo.Amount)
		if err != nil {
			return nil, fmt.Errorf("utxo %s:%d: %w", utxo.TxID, utxo.Vout, err)
		}
		if !sats.IsInt64() {
			return nil, fmt.Errorf("utxo %s:%d: %s sats overflow int64", utxo.TxID, utxo.Vout, sats.String())
		}
		out = append(out, btcInput{TxID: utxo.TxID, Vout: utxo.Vout, Value: sats.Int64(), Address: address})
	}
	return out, nil
}

func (a *BitcoinLive) broadcastBitcoin(ctx context.Context, signed *types.SignedTx) (string, error) {
	if signed == nil || len(signed.RawBytes) == 0 {
		return "", fmt.Errorf("btc broadcast: empty signed transaction")
	}
	if a.restAPI {
		url := strings.TrimRight(a.cfg.RPCURL, "/") + "/tx"
		bodyHex := hex.EncodeToString(signed.RawBytes)
		resp, err := a.http.Do(ctx, httpclient.Request{
			Method:  httpclient.MethodPost,
			URL:     url,
			Body:    []byte(bodyHex),
			HasBody: true,
		})
		if err != nil {
			return "", err
		}
		if resp.StatusCode >= httpclient.StatusMultipleChoices {
			return "", fmt.Errorf("btc broadcast %d: %s", resp.StatusCode, resp.Body)
		}
		return strings.TrimSpace(string(resp.Body)), nil
	}
	rawHex := hex.EncodeToString(signed.RawBytes)
	var txHash string
	if err := a.rpc.Call(ctx, "sendrawtransaction", &txHash, rawHex); err != nil {
		return "", err
	}
	return txHash, nil
}

func hash256(b []byte) []byte {
	first := sha256.Sum256(b)
	second := sha256.Sum256(first[:])
	return second[:]
}
