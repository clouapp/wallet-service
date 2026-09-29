package chain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"

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

func (a *BitcoinLive) btcFeeSats() int64 {
	rate := btcDefaultFeeRate
	if a.cfg.FeeRateDefault > 0 {
		rate = a.cfg.FeeRateDefault
	}
	return int64(btcFeeVBytes * rate)
}

func (a *BitcoinLive) buildBitcoinTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	if req.Amount == nil || !req.Amount.IsInt64() || req.Amount.Sign() <= 0 {
		return nil, fmt.Errorf("amount is required")
	}
	return a.assembleBitcoinTx(ctx, req.From, req.To, req.Amount.Int64())
}

func (a *BitcoinLive) buildBitcoinSweep(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
	utxos, err := a.listConfirmedUTXOs(ctx, req.From)
	if err != nil {
		return nil, err
	}
	var sum int64
	for _, in := range utxos {
		sum += in.Value
	}
	fee := a.btcFeeSats()
	amount := sum - fee
	if req.Amount != nil {
		if !req.Amount.IsInt64() || req.Amount.Sign() <= 0 {
			return nil, fmt.Errorf("amount is required")
		}
		amount = req.Amount.Int64()
	}
	if len(utxos) == 0 || amount <= 0 || amount+fee > sum {
		return nil, fmt.Errorf("insufficient funds")
	}
	unsigned := &types.UnsignedTx{
		ChainID: a.cfg.ChainIDStr,
		Metadata: map[string]interface{}{
			"inputs":  utxos,
			"outputs": btcPaymentOutputs(req.From, req.To, amount, fee, sum),
			"testnet": a.cfg.IsTestnet,
		},
	}
	return []types.UnsignedTx{*unsigned}, nil
}

func (a *BitcoinLive) assembleBitcoinTx(ctx context.Context, from, to string, amount int64) (*types.UnsignedTx, error) {
	utxos, err := a.listConfirmedUTXOs(ctx, from)
	if err != nil {
		return nil, err
	}
	fee := a.btcFeeSats()
	selected, sum, err := selectBTCUTXOs(utxos, amount+fee)
	if err != nil {
		return nil, err
	}
	return &types.UnsignedTx{
		ChainID: a.cfg.ChainIDStr,
		Metadata: map[string]interface{}{
			"inputs":  selected,
			"outputs": btcPaymentOutputs(from, to, amount, fee, sum),
			"testnet": a.cfg.IsTestnet,
		},
	}, nil
}

func btcPaymentOutputs(from, to string, amount, fee, sum int64) []btcOutput {
	change := sum - amount - fee
	if change >= btcDustSats {
		return []btcOutput{{Address: to, Value: amount}, {Address: from, Value: change}}
	}
	return []btcOutput{{Address: to, Value: amount}}
}

func selectBTCUTXOs(utxos []btcInput, need int64) ([]btcInput, int64, error) {
	var selected []btcInput
	var sum int64
	for _, utxo := range utxos {
		selected = append(selected, utxo)
		sum += utxo.Value
		if sum >= need {
			return selected, sum, nil
		}
	}
	return nil, sum, fmt.Errorf("insufficient funds")
}

func (a *BitcoinLive) listConfirmedUTXOs(ctx context.Context, address string) ([]btcInput, error) {
	if a.restAPI {
		return a.listUTXOsREST(ctx, address)
	}
	return a.listUTXOsRPC(ctx, address)
}

func (a *BitcoinLive) listUTXOsREST(ctx context.Context, address string) ([]btcInput, error) {
	url := strings.TrimRight(a.cfg.RPCURL, "/") + "/address/" + address + "/utxo"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("btc utxo %d: %s", resp.StatusCode, body)
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
		TxID   string  `json:"txid"`
		Vout   uint32  `json:"vout"`
		Amount float64 `json:"amount"`
	}
	if err := a.rpc.Call(ctx, "listunspent", &raw, 1, 9999999, []string{address}); err != nil {
		return nil, err
	}
	out := make([]btcInput, 0, len(raw))
	for _, utxo := range raw {
		sats := int64(math.Round(utxo.Amount * 1e8))
		out = append(out, btcInput{TxID: utxo.TxID, Vout: utxo.Vout, Value: sats, Address: address})
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
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(bodyHex))
		if err != nil {
			return "", err
		}
		resp, err := a.http.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		if resp.StatusCode >= 300 {
			return "", fmt.Errorf("btc broadcast %d: %s", resp.StatusCode, body)
		}
		return strings.TrimSpace(string(body)), nil
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
