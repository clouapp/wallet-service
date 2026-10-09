package bitcoin

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// Electrum (ElectrumX protocol 1.4) fallback provider: newline-delimited JSON-RPC
// over TLS. It serves balances, UTXOs, the tip, fee rates, transaction status and
// broadcast by address script hash; it cannot list a block's transactions, so block
// scans stay on the other providers.
const (
	electrumSSLScheme = "electrum+ssl://"
	electrumTCPScheme = "electrum+tcp://"
	// electrumCertPinParam is the hex SHA-256 of the server's leaf certificate. Public
	// Electrum servers use self-signed certificates, so the pin, not a CA, is what
	// authenticates them; without it the system roots must verify the certificate.
	electrumCertPinParam = "cert_sha256"

	electrumProtocolVersion = "1.4"
	electrumClientName      = "macro-wallets"
	electrumCallTimeout     = 20 * time.Second
	electrumMaxLineBytes    = 16 << 20

	// ElectrumX answers code 1 to a transaction the daemon refused under its rules,
	// and passes the daemon's -5 for a txid it does not know.
	electrumRejectedCode = 1
	electrumTxNotFound   = "No such mempool or blockchain transaction"

	// estimatefee answers -1 when the daemon has no estimate.
	electrumNoEstimate = -1

	blockHeaderBytes = 80
)

func isElectrumURL(rawURL string) bool {
	return strings.HasPrefix(rawURL, electrumSSLScheme) || strings.HasPrefix(rawURL, electrumTCPScheme)
}

type electrumProvider struct {
	name    string
	address string
	tls     *tls.Config
	network bitcoinNetwork
	asset   string
	timeout time.Duration
	dialer  net.Dialer
	// genesisVerified is set once server.features proved the network.
	genesisVerified *atomic.Bool
}

// newElectrumProvider parses electrum+ssl://host:port?cert_sha256=HEX (or
// electrum+tcp:// to a loopback host, for tests and a local server).
func newElectrumProvider(name, rawURL string, network bitcoinNetwork, asset string) (*electrumProvider, error) {
	parsed, err := url.Parse(strings.Replace(rawURL, "electrum+", "", 1))
	if err != nil {
		return nil, fmt.Errorf("electrum URL: %w", withoutURL(err))
	}
	if parsed.Hostname() == "" || parsed.Port() == "" {
		return nil, fmt.Errorf("electrum URL needs host:port")
	}
	provider := &electrumProvider{
		name: name, address: parsed.Host, network: network, asset: asset,
		timeout: electrumCallTimeout, genesisVerified: &atomic.Bool{},
	}
	switch parsed.Scheme {
	case "ssl":
		tlsConfig, err := electrumTLSConfig(parsed.Hostname(), parsed.Query().Get(electrumCertPinParam))
		if err != nil {
			return nil, err
		}
		provider.tls = tlsConfig
	case "tcp":
		if ip := net.ParseIP(parsed.Hostname()); parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("plaintext electrum is only allowed to a loopback host; use %s", electrumSSLScheme)
		}
	default:
		return nil, fmt.Errorf("electrum URL scheme %q", parsed.Scheme)
	}
	return provider, nil
}

// electrumTLSConfig verifies the leaf certificate against pin when one is given, and
// against the system roots otherwise.
func electrumTLSConfig(host, pin string) (*tls.Config, error) {
	config := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if pin == "" {
		return config, nil
	}
	want, err := hex.DecodeString(strings.ReplaceAll(strings.ToLower(pin), ":", ""))
	if err != nil || len(want) != sha256.Size {
		return nil, fmt.Errorf("%s must be the 64-character hex SHA-256 of the certificate", electrumCertPinParam)
	}
	// The pin replaces chain verification: VerifyPeerCertificate below is the check.
	config.InsecureSkipVerify = true
	config.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("electrum server sent no certificate")
		}
		got := sha256.Sum256(rawCerts[0])
		if subtle.ConstantTimeCompare(got[:], want) != 1 {
			return fmt.Errorf("electrum certificate sha256 %x does not match the pin", got)
		}
		return nil
	}
	return config, nil
}

func (p *electrumProvider) label() string { return p.name + " (electrum)" }

// electrumSession is one connection: requests are sent and answered one at a time.
type electrumSession struct {
	conn   net.Conn
	reader *bufio.Reader
	nextID uint64
}

type electrumResponse struct {
	ID     *uint64         `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *electrumError  `json:"error"`
}

type electrumError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *electrumError) Error() string {
	return fmt.Sprintf("electrum error %d: %s", e.Code, e.Message)
}

// session dials, negotiates the protocol version and, once per provider, checks the
// server's genesis block. The deadline is the context's, or electrumCallTimeout.
func (p *electrumProvider) session(ctx context.Context) (*electrumSession, error) {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > p.timeout {
		deadline = time.Now().Add(p.timeout)
	}
	dialCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var conn net.Conn
	var err error
	if p.tls != nil {
		tlsDialer := tls.Dialer{NetDialer: &p.dialer, Config: p.tls}
		conn, err = tlsDialer.DialContext(dialCtx, "tcp", p.address)
	} else {
		conn, err = p.dialer.DialContext(dialCtx, "tcp", p.address)
	}
	if err != nil {
		return nil, fmt.Errorf("electrum dial: %w", err)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, err
	}
	s := &electrumSession{conn: conn, reader: bufio.NewReaderSize(conn, 64<<10)}
	if err := s.call("server.version", nil, electrumClientName, electrumProtocolVersion); err != nil {
		s.close()
		return nil, err
	}
	if err := p.verifyGenesis(s); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func (p *electrumProvider) verifyGenesis(s *electrumSession) error {
	if p.network.genesisHash == "" || p.genesisVerified.Load() {
		return nil
	}
	var features struct {
		GenesisHash string `json:"genesis_hash"`
	}
	if err := s.call("server.features", &features); err != nil {
		return err
	}
	if !strings.EqualFold(features.GenesisHash, p.network.genesisHash) {
		return fmt.Errorf("electrum server serves genesis %q, not %s's %s", features.GenesisHash, p.network.name, p.network.genesisHash)
	}
	p.genesisVerified.Store(true)
	return nil
}

func (s *electrumSession) close() { _ = s.conn.Close() }

// call sends one request and reads lines until its answer, skipping notifications.
func (s *electrumSession) call(method string, out any, params ...any) error {
	if params == nil {
		params = []any{}
	}
	s.nextID++
	id := s.nextID
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return fmt.Errorf("encode electrum %s: %w", method, err)
	}
	if _, err := s.conn.Write(append(request, '\n')); err != nil {
		return fmt.Errorf("electrum %s: %w", method, err)
	}
	for {
		line, err := s.readLine()
		if err != nil {
			return fmt.Errorf("electrum %s: %w", method, err)
		}
		var resp electrumResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			return fmt.Errorf("electrum %s: parse answer: %w", method, err)
		}
		if resp.ID == nil || *resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("electrum %s: parse result: %w", method, err)
		}
		return nil
	}
}

func (s *electrumSession) readLine() ([]byte, error) {
	var line []byte
	for {
		chunk, isPrefix, err := s.reader.ReadLine()
		if err != nil {
			return nil, err
		}
		line = append(line, chunk...)
		if len(line) > electrumMaxLineBytes {
			return nil, fmt.Errorf("answer larger than %d bytes", electrumMaxLineBytes)
		}
		if !isPrefix {
			return line, nil
		}
	}
}

// withSession runs fn on a fresh session and closes it.
func withSession[T any](ctx context.Context, p *electrumProvider, fn func(*electrumSession) (T, error)) (T, error) {
	var zero T
	s, err := p.session(ctx)
	if err != nil {
		return zero, err
	}
	defer s.close()
	return fn(s)
}

// scriptHash is the Electrum script hash of a P2WPKH address of this network:
// sha256(0x00 0x14 <program>), byte-reversed, hex.
func (p *electrumProvider) scriptHash(address string) (string, error) {
	program, err := witnessProgram(address, p.network.params)
	if err != nil {
		return "", err
	}
	script := append([]byte{p2wpkhWitnessVersion, p2wpkhProgramSize}, program...)
	sum := sha256.Sum256(script)
	for i, j := 0, len(sum)-1; i < j; i, j = i+1, j-1 {
		sum[i], sum[j] = sum[j], sum[i]
	}
	return hex.EncodeToString(sum[:]), nil
}

func (p *electrumProvider) balance(ctx context.Context, address string) (*types.Balance, error) {
	hash, err := p.scriptHash(address)
	if err != nil {
		return nil, err
	}
	return withSession(ctx, p, func(s *electrumSession) (*types.Balance, error) {
		var got struct {
			Confirmed   int64 `json:"confirmed"`
			Unconfirmed int64 `json:"unconfirmed"`
		}
		if err := s.call("blockchain.scripthash.get_balance", &got, hash); err != nil {
			return nil, err
		}
		total := big.NewInt(got.Confirmed + got.Unconfirmed)
		if total.Sign() < 0 {
			return nil, fmt.Errorf("electrum balance of %s is negative (%s)", address, total)
		}
		return &types.Balance{Address: address, Asset: p.asset, Amount: total, Decimals: btcDecimals, Human: fmtUnits(total, btcDecimals)}, nil
	})
}

func (p *electrumProvider) confirmedUTXOs(ctx context.Context, address string) ([]btcInput, error) {
	hash, err := p.scriptHash(address)
	if err != nil {
		return nil, err
	}
	return withSession(ctx, p, func(s *electrumSession) ([]btcInput, error) {
		var raw []struct {
			TxHash string `json:"tx_hash"`
			TxPos  uint32 `json:"tx_pos"`
			Height int64  `json:"height"`
			Value  int64  `json:"value"`
		}
		if err := s.call("blockchain.scripthash.listunspent", &raw, hash); err != nil {
			return nil, err
		}
		out := make([]btcInput, 0, len(raw))
		for _, utxo := range raw {
			if utxo.Height <= 0 {
				continue
			}
			txID, err := normalizeBTCHash(utxo.TxHash, "txid")
			if err != nil {
				return nil, err
			}
			out = append(out, btcInput{TxID: txID, Vout: utxo.TxPos, Value: utxo.Value, Address: address})
		}
		return out, nil
	})
}

func (p *electrumProvider) latestBlock(ctx context.Context) (uint64, error) {
	return withSession(ctx, p, tipHeight)
}

func tipHeight(s *electrumSession) (uint64, error) {
	var tip struct {
		Height uint64 `json:"height"`
	}
	if err := s.call("blockchain.headers.subscribe", &tip); err != nil {
		return 0, err
	}
	if tip.Height == 0 {
		return 0, errors.New("electrum tip has no height")
	}
	return tip.Height, nil
}

func (p *electrumProvider) scanBlock(context.Context, uint64) ([]types.DetectedTransfer, error) {
	return nil, errProviderUnsupported
}

// transactionBlock reads the verbose transaction (confirmations and block hash from
// the daemon) and the tip, derives the height and accepts it only if the header at
// that height hashes to the transaction's block, so the index and the daemon agree.
func (p *electrumProvider) transactionBlock(ctx context.Context, txID string) (uint64, error) {
	return withSession(ctx, p, func(s *electrumSession) (uint64, error) {
		var tx struct {
			BlockHash     string `json:"blockhash"`
			Confirmations uint64 `json:"confirmations"`
		}
		if err := s.call("blockchain.transaction.get", &tx, txID, true); err != nil {
			var electrumErr *electrumError
			if errors.As(err, &electrumErr) && strings.Contains(electrumErr.Message, electrumTxNotFound) {
				return 0, nil
			}
			return 0, err
		}
		if tx.Confirmations == 0 || tx.BlockHash == "" {
			return 0, nil
		}
		tip, err := tipHeight(s)
		if err != nil {
			return 0, err
		}
		if tx.Confirmations > tip {
			return 0, fmt.Errorf("electrum: tx %s has %d confirmations above tip %d", txID, tx.Confirmations, tip)
		}
		height := tip - tx.Confirmations + 1
		var headerHex string
		if err := s.call("blockchain.block.header", &headerHex, height); err != nil {
			return 0, err
		}
		header, err := hex.DecodeString(headerHex)
		if err != nil || len(header) != blockHeaderBytes {
			return 0, fmt.Errorf("electrum: header %d is not %d bytes of hex", height, blockHeaderBytes)
		}
		blockHash := reversedHex(hash256(header))
		if !strings.EqualFold(blockHash, tx.BlockHash) {
			return 0, fmt.Errorf("electrum: header %d is block %s, tx %s is in %s; index and daemon disagree", height, blockHash, txID, tx.BlockHash)
		}
		return height, nil
	})
}

func reversedHex(b []byte) string {
	reversed := bytes.Clone(b)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return hex.EncodeToString(reversed)
}

// feeRate is estimatefee for btcSmartFeeTargetBlocks, in coin per kvB like bitcoind's
// estimatesmartfee.
func (p *electrumProvider) feeRate(ctx context.Context) (int64, error) {
	return withSession(ctx, p, func(s *electrumSession) (int64, error) {
		var perKvB float64
		if err := s.call("blockchain.estimatefee", &perKvB, btcSmartFeeTargetBlocks); err != nil {
			return 0, err
		}
		if perKvB == electrumNoEstimate {
			return 0, errors.New("electrum has no fee estimate")
		}
		return milliSatRate(decimal.NewFromFloat(perKvB*float64(satsPerBTC/milliSatsPerSat)), "electrum estimatefee")
	})
}

func (p *electrumProvider) broadcast(ctx context.Context, raw []byte) (string, error) {
	return withSession(ctx, p, func(s *electrumSession) (string, error) {
		var txID string
		err := s.call("blockchain.transaction.broadcast", &txID, hex.EncodeToString(raw))
		var electrumErr *electrumError
		switch {
		case err == nil:
		case errors.As(err, &electrumErr) && electrumErr.Code == electrumRejectedCode:
			return "", &btcBroadcastRejectedError{provider: p.label(), reason: electrumErr.Message}
		case errors.As(err, &electrumErr):
			return "", err
		default:
			// The request was written; a timeout or a dropped connection says nothing
			// about whether the server relayed it, so reconcile instead of re-sending.
			return "", chain.UnknownOutcome(err)
		}
		return txID, nil
	})
}
