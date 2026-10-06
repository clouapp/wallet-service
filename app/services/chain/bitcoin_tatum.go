package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// Tatum (tatum.io) fallback provider. Its RPC gateway (https://<chain>-<network>.
// gateway.tatum.io, bitcoind JSON-RPC) serves the tip, block scans, transaction
// status, fee rate, paid fees and broadcast. No gateway lists an address's UTXOs
// (listunspent needs a wallet), so with an API key the Data API (/v4/data/utxos)
// answers balance and UTXO lookups; without one it answers 401 and those calls are
// left to the other providers. Keyless, the gateway allows 5 requests a minute; a
// key raises it to the plan's limit (5 per second on the free plan).
//
// The key goes out as the x-api-key header to Tatum only (the gateway URL must be a
// *.tatum.io host; the Data API URL is the configured one) and is redacted from every
// error, since Tatum echoes an invalid key back in its 401 body.
const (
	tatumHostSuffix = ".tatum.io"
	// DefaultTatumDataAPIURL is the Data API base when a key is set and
	// <PREFIX>_TATUM_DATA_API_URL is empty.
	DefaultTatumDataAPIURL = "https://api.tatum.io"
	tatumUTXOsPath         = "/v4/data/utxos"
	// tatumUTXOTotalValueAll makes /v4/data/utxos return every UTXO of the address: it
	// answers UTXOs up to totalValue coins, and this is Litecoin's supply cap (above
	// Bitcoin's).
	tatumUTXOTotalValueAll = "84000000"
	// tatumMaxVerifiedUTXOs bounds the gettxout checks behind one UTXO listing; an
	// address with more is left to the other providers.
	tatumMaxVerifiedUTXOs = 200
	tatumMaxResponseBytes = 8 << 20
	tatumErrorBodyBytes   = 300
	tatumProviderKind     = "tatum"

	// gettxout's include_mempool: an output spent by a mempool transaction answers
	// null, and one created there has 0 confirmations; neither is offered for spending.
	bitcoindIncludeMempool = true

	redactedSecret = "[redacted]"
)

// tatumDataChains are the Data API chain names of the networks it indexes. Bitcoin
// testnet4 has none, so its UTXOs are never read from a testnet3 index.
var tatumDataChains = map[string]string{
	models.NetworkBitcoinMainnet:  "bitcoin",
	models.NetworkBitcoinTestnet:  "bitcoin-testnet",
	models.NetworkLitecoinMainnet: "litecoin",
	models.NetworkLitecoinTestnet: "litecoin-testnet",
}

// isTatumURL reports an https URL on a *.tatum.io host, the only fallback the API
// key is sent to.
func isTatumURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	return strings.HasSuffix(strings.ToLower(parsed.Hostname()), tatumHostSuffix)
}

// tatumProvider is the gateway (genesis-checked like every JSON-RPC fallback) plus,
// when a key is configured, the Data API for UTXOs and balance.
type tatumProvider struct {
	gateway directProvider
	data    *tatumDataAPI
}

func newTatumProvider(gateway directProvider, data *tatumDataAPI) tatumProvider {
	gateway.kind = tatumProviderKind
	return tatumProvider{gateway: gateway, data: data}
}

func (p tatumProvider) label() string { return p.gateway.label() }

func (p tatumProvider) balance(ctx context.Context, address string) (*types.Balance, error) {
	utxos, err := p.dataUTXOs(ctx, address)
	if err != nil {
		return nil, err
	}
	total := big.NewInt(0)
	for _, utxo := range utxos {
		total.Add(total, big.NewInt(utxo.Value))
	}
	asset := p.gateway.live.cfg.NativeSymbol
	return &types.Balance{Address: address, Asset: asset, Amount: total, Decimals: btcDecimals, Human: fmtUnits(total, btcDecimals)}, nil
}

// confirmedUTXOs are the Data API's UTXOs that the gateway's node still holds
// unspent, confirmed, for the same value and address (gettxout).
func (p tatumProvider) confirmedUTXOs(ctx context.Context, address string) ([]btcInput, error) {
	utxos, err := p.dataUTXOs(ctx, address)
	if err != nil {
		return nil, err
	}
	if len(utxos) > tatumMaxVerifiedUTXOs {
		return nil, fmt.Errorf("%w: %d UTXOs to verify, above %d", errProviderUnsupported, len(utxos), tatumMaxVerifiedUTXOs)
	}
	confirmed := make([]btcInput, 0, len(utxos))
	for _, utxo := range utxos {
		var out *struct {
			Confirmations uint64          `json:"confirmations"`
			Value         decimal.Decimal `json:"value"`
			ScriptPubKey  struct {
				Address   string   `json:"address"`
				Addresses []string `json:"addresses"`
			} `json:"scriptPubKey"`
		}
		if err := p.gateway.live.rpc.Call(ctx, "gettxout", &out, utxo.TxID, utxo.Vout, bitcoindIncludeMempool); err != nil {
			return nil, fmt.Errorf("gettxout %s:%d: %w", utxo.TxID, utxo.Vout, unsupportedWhenMethodNotFound(err))
		}
		if out == nil || out.Confirmations == 0 {
			continue
		}
		sats, err := btcToSats(out.Value)
		if err != nil {
			return nil, fmt.Errorf("gettxout %s:%d: %w", utxo.TxID, utxo.Vout, err)
		}
		holder := out.ScriptPubKey.Address
		if holder == "" && len(out.ScriptPubKey.Addresses) == 1 {
			holder = out.ScriptPubKey.Addresses[0]
		}
		if !sats.IsInt64() || sats.Int64() != utxo.Value || holder != address {
			return nil, fmt.Errorf("tatum: data api lists %s:%d as %d sats to %s, the node holds %s sats to %q",
				utxo.TxID, utxo.Vout, utxo.Value, address, sats, holder)
		}
		confirmed = append(confirmed, utxo)
	}
	return confirmed, nil
}

func (p tatumProvider) dataUTXOs(ctx context.Context, address string) ([]btcInput, error) {
	if p.data == nil {
		return nil, errProviderUnsupported
	}
	if err := p.gateway.verifyNetwork(ctx); err != nil {
		return nil, err
	}
	return p.data.utxos(ctx, address)
}

func (p tatumProvider) latestBlock(ctx context.Context) (uint64, error) {
	return p.gateway.latestBlock(ctx)
}

func (p tatumProvider) scanBlock(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	return p.gateway.scanBlock(ctx, blockNum)
}

func (p tatumProvider) transactionBlock(ctx context.Context, txID string) (uint64, error) {
	return p.gateway.transactionBlock(ctx, txID)
}

func (p tatumProvider) feeRate(ctx context.Context) (int64, error) { return p.gateway.feeRate(ctx) }

func (p tatumProvider) transactionFee(ctx context.Context, txID string) (int64, error) {
	return p.gateway.transactionFee(ctx, txID)
}

func (p tatumProvider) broadcast(ctx context.Context, raw []byte) (string, error) {
	return p.gateway.broadcast(ctx, raw)
}

// tatumDataAPI reads an address's UTXOs from Tatum's Data API.
type tatumDataAPI struct {
	baseURL string
	apiKey  string
	chain   string
	http    *http.Client
}

// newTatumDataAPI is nil without a key or for a network the Data API does not index.
func newTatumDataAPI(baseURL, apiKey, network string, client *http.Client) (*tatumDataAPI, error) {
	chain, indexed := tatumDataChains[network]
	if apiKey == "" || !indexed {
		return nil, nil
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultTatumDataAPIURL
	}
	if err := requireSecureURL(baseURL); err != nil {
		return nil, fmt.Errorf("tatum data api URL: %w", err)
	}
	return &tatumDataAPI{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), apiKey: apiKey, chain: chain, http: client}, nil
}

// requireSecureURL accepts https, and plain http to a loopback host only (tests, a
// local proxy): the URL carries an API key in its headers.
func requireSecureURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return withoutURL(err)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(parsed.Hostname()); parsed.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return fmt.Errorf("want https:// (http:// only to a loopback host)")
}

func (d *tatumDataAPI) utxos(ctx context.Context, address string) ([]btcInput, error) {
	query := url.Values{"chain": {d.chain}, "address": {address}, "totalValue": {tatumUTXOTotalValueAll}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.baseURL+tatumUTXOsPath+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("tatum data api utxos: %w", withoutURL(err))
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(apiKeyHeader, d.apiKey)
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tatum data api utxos: %w", withoutURL(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, tatumMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("tatum data api utxos: read: %w", withoutURL(err))
	}
	if len(body) > tatumMaxResponseBytes {
		return nil, fmt.Errorf("tatum data api utxos: response larger than %d bytes", tatumMaxResponseBytes)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("tatum data api utxos: %w (HTTP %d)", ErrRateLimited, resp.StatusCode)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("tatum data api utxos: HTTP %d: %s", resp.StatusCode, d.redact(body))
	}
	var raw []struct {
		TxHash        string `json:"txHash"`
		Index         uint32 `json:"index"`
		Address       string `json:"address"`
		ValueAsString string `json:"valueAsString"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("tatum data api utxos: parse: %w", err)
	}
	utxos := make([]btcInput, 0, len(raw))
	for _, utxo := range raw {
		if utxo.Address != "" && utxo.Address != address {
			return nil, fmt.Errorf("tatum data api utxos: asked for %s, got a UTXO of %s", address, utxo.Address)
		}
		txID, err := normalizeBTCHash(strings.TrimPrefix(utxo.TxHash, "0x"), "txid")
		if err != nil {
			return nil, fmt.Errorf("tatum data api utxos: %w", err)
		}
		coins, err := decimal.NewFromString(utxo.ValueAsString)
		if err != nil {
			return nil, fmt.Errorf("tatum data api utxos: value %q of %s:%d: %w", utxo.ValueAsString, txID, utxo.Index, err)
		}
		sats, err := btcToSats(coins)
		if err != nil || sats.Sign() <= 0 || !sats.IsInt64() {
			return nil, fmt.Errorf("tatum data api utxos: value %q of %s:%d is not a positive amount", utxo.ValueAsString, txID, utxo.Index)
		}
		utxos = append(utxos, btcInput{TxID: txID, Vout: utxo.Index, Value: sats.Int64(), Address: address})
	}
	return utxos, nil
}

func (d *tatumDataAPI) redact(body []byte) string {
	text := strings.TrimSpace(redactSecrets(string(body), d.apiKey))
	if len(text) > tatumErrorBodyBytes {
		text = text[:tatumErrorBodyBytes] + "…"
	}
	return text
}

// redactSecrets replaces every non-empty secret in text.
func redactSecrets(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, redactedSecret)
		}
	}
	return text
}
