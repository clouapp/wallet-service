package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/macrowallets/waas/pkg/pyjson"
)

const (
	TronNileURL        = "https://nile.trongrid.io"
	LitecoinTestnetURL = "https://litecoinspace.org/testnet/api"
	ChainVerifyTimeout = 30 * time.Second

	networkProfileKey     = "CHAIN_NETWORK_PROFILE"
	testnetProfile        = "testnet"
	rpcURLEnvSuffix       = "_RPC_URL"
	tronInfoPath          = "/wallet/gettransactioninfobyid"
	tronTransactionPath   = "/wallet/gettransactionbyid"
	tronSuccess           = "SUCCESS"
	evmReceiptMethod      = "eth_getTransactionReceipt"
	evmReceiptSuccess     = "0x1"
	jsonRPCVersion        = "2.0"
	hexPrefix             = "0x"
	hexBase               = 16
	maxChainResponseBytes = 1 << 20
	redactedHost          = "<redacted host>"
)

var (
	tronChains     = map[string]bool{"tron": true, "ttron": true}
	litecoinChains = map[string]bool{"ltc": true, "tltc": true}
	evmChains      = map[string]bool{
		"eth": true, "teth": true, "polygon": true, "tpolygon": true, "base": true, "tbase": true,
		"arbitrum": true, "tarbitrum": true, "bsc": true, "tbsc": true,
	}
)

// Verification is the read-only on-chain view of one tx hash.
type Verification struct {
	Confirmed bool
	Note      string
}

// ChainVerifier reads transaction outcomes from public testnet endpoints (TRON Nile,
// litecoinspace testnet) and the EVM RPC of the e2e environ. Nothing is ever sent:
// only receipt/status reads. EVM RPC URLs may carry API keys and never reach output.
type ChainVerifier struct {
	TronURL     string
	LitecoinURL string
	EVMRPCURL   func(chain string) (string, error)
	HTTP        *http.Client
}

// ChainVerifierFromEnviron builds the verifier for the e2e environ, which must use the
// testnet profile (the TRON and Litecoin endpoints are testnet ones).
func ChainVerifierFromEnviron(environment map[string]string) (ChainVerifier, error) {
	if profile := environment[networkProfileKey]; profile != testnetProfile {
		return ChainVerifier{}, fmt.Errorf("%s=%s in the e2e environ; on-chain verification is testnet-only", networkProfileKey, pythonRepr(profile))
	}
	return ChainVerifier{
		TronURL:     TronNileURL,
		LitecoinURL: LitecoinTestnetURL,
		EVMRPCURL: func(chain string) (string, error) {
			name := strings.ToUpper(chain) + rpcURLEnvSuffix
			url := strings.TrimSpace(environment[name])
			if url == "" {
				return "", fmt.Errorf("%s is not set in the e2e environ", name)
			}
			return url, nil
		},
		HTTP: &http.Client{Timeout: ChainVerifyTimeout},
	}, nil
}

// Verify reports whether hash succeeded on chain (a vault_test chain id).
func (verifier ChainVerifier) Verify(ctx context.Context, chain, hash string) (Verification, error) {
	if _, err := Require(TxHashPattern, hash, "tx hash"); err != nil {
		return Verification{}, err
	}
	switch {
	case tronChains[chain]:
		return verifier.verifyTron(ctx, hash)
	case litecoinChains[chain]:
		return verifier.verifyLitecoin(ctx, hash)
	case evmChains[chain]:
		return verifier.verifyEVM(ctx, chain, hash)
	default:
		return Verification{}, fmt.Errorf("no read-only verifier for chain %s", pythonRepr(chain))
	}
}

// verifyTron: receipt.result SUCCESS (contract calls), else ret[0].contractRet SUCCESS
// (native transfers carry no receipt.result); both need a blockNumber.
func (verifier ChainVerifier) verifyTron(ctx context.Context, hash string) (Verification, error) {
	body := pyjson.Object{{Key: "value", Value: hash}}
	info, err := verifier.postJSON(ctx, verifier.TronURL+tronInfoPath, body, "TRON Nile")
	if err != nil {
		return Verification{}, err
	}
	if len(info) == 0 {
		return Verification{Note: "TRON Nile: transaction not found yet"}, nil
	}
	block := numberText(info, "blockNumber")
	receipt, _ := info.Get("receipt")
	receiptObject, _ := receipt.(pyjson.Object)
	result := receiptObject.String("result")
	if result == "" {
		transaction, err := verifier.postJSON(ctx, verifier.TronURL+tronTransactionPath, body, "TRON Nile")
		if err != nil {
			return Verification{}, err
		}
		result = tronContractRet(transaction)
		if result == "" {
			return Verification{Note: "TRON Nile: no receipt.result nor contractRet yet"}, nil
		}
		return Verification{Confirmed: result == tronSuccess && block != "", Note: fmt.Sprintf("TRON Nile block %s, contractRet %s", orNone(block), result)}, nil
	}
	return Verification{Confirmed: result == tronSuccess && block != "", Note: fmt.Sprintf("TRON Nile block %s, receipt.result %s", orNone(block), result)}, nil
}

func tronContractRet(transaction pyjson.Object) string {
	ret, _ := transaction.Get("ret")
	items, _ := ret.([]any)
	if len(items) == 0 {
		return ""
	}
	first, _ := items[0].(pyjson.Object)
	return first.String("contractRet")
}

func (verifier ChainVerifier) verifyLitecoin(ctx context.Context, hash string) (Verification, error) {
	status, err := verifier.getJSON(ctx, verifier.LitecoinURL+"/tx/"+hash+"/status", "litecoinspace testnet")
	if err != nil {
		return Verification{}, err
	}
	confirmed, _ := status.Get("confirmed")
	if confirmed != true {
		return Verification{Note: "litecoinspace testnet: not confirmed yet"}, nil
	}
	return Verification{Confirmed: true, Note: "litecoinspace testnet block " + orNone(numberText(status, "block_height")) + ", confirmed"}, nil
}

func (verifier ChainVerifier) verifyEVM(ctx context.Context, chain, hash string) (Verification, error) {
	if verifier.EVMRPCURL == nil {
		return Verification{}, fmt.Errorf("no EVM RPC configured")
	}
	rpcURL, err := verifier.EVMRPCURL(chain)
	if err != nil {
		return Verification{}, err
	}
	label := chain + " RPC"
	reply, err := verifier.postJSON(ctx, rpcURL, pyjson.Object{
		{Key: "jsonrpc", Value: jsonRPCVersion},
		{Key: "id", Value: 1},
		{Key: "method", Value: evmReceiptMethod},
		{Key: "params", Value: []any{hash}},
	}, label)
	if err != nil {
		return Verification{}, err
	}
	if rpcError, present := reply.Get("error"); present && rpcError != nil {
		return Verification{}, fmt.Errorf("%s %s failed: %s", label, evmReceiptMethod, truncateRunes(dumpsOrEmpty(rpcError), errorResponseRunes))
	}
	result, _ := reply.Get("result")
	receipt, isObject := result.(pyjson.Object)
	if !isObject {
		return Verification{Note: label + ": no receipt yet"}, nil
	}
	status := receipt.String("status")
	block := hexToDecimal(receipt.String("blockNumber"))
	return Verification{Confirmed: status == evmReceiptSuccess && block != "", Note: fmt.Sprintf("%s block %s, receipt status %s", label, orNone(block), orNone(status))}, nil
}

func (verifier ChainVerifier) postJSON(ctx context.Context, url string, body pyjson.Object, label string) (pyjson.Object, error) {
	encoded, err := pyjson.Dumps(body, pyjson.Default)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte(encoded)))
	if err != nil {
		return nil, fmt.Errorf("build %s request failed", label)
	}
	request.Header.Set("Content-Type", "application/json")
	return verifier.do(request, label)
}

func (verifier ChainVerifier) getJSON(ctx context.Context, url, label string) (pyjson.Object, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build %s request failed", label)
	}
	return verifier.do(request, label)
}

// do never puts the URL in an error: EVM RPC URLs may carry API keys.
func (verifier ChainVerifier) do(request *http.Request, label string) (pyjson.Object, error) {
	request.Header.Set("Accept", "application/json")
	client := verifier.HTTP
	if client == nil {
		client = &http.Client{Timeout: ChainVerifyTimeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s request failed: %s", label, redactRequestURL(err, request))
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxChainResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %s", label, redactRequestURL(err, request))
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered HTTP %d", label, response.StatusCode)
	}
	decoded, err := decodeObject(raw)
	if err != nil {
		return nil, fmt.Errorf("%s answered something that is not a JSON object", label)
	}
	return decoded, nil
}

// redactRequestURL drops the URL and its host from a transport error (DNS and TLS
// errors name the host, which some providers derive from the API key).
func redactRequestURL(err error, request *http.Request) string {
	message := redactURLError(err).Error()
	for _, secret := range []string{request.URL.String(), request.URL.Host, request.URL.Hostname()} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, redactedHost)
		}
	}
	return message
}

func numberText(object pyjson.Object, key string) string {
	value, _ := object.Get(key)
	if number, isNumber := value.(json.Number); isNumber {
		return number.String()
	}
	return ""
}

func hexToDecimal(value string) string {
	digits, found := strings.CutPrefix(value, hexPrefix)
	if !found || digits == "" {
		return ""
	}
	number, ok := new(big.Int).SetString(digits, hexBase)
	if !ok {
		return ""
	}
	return number.String()
}

func orNone(value string) string {
	if value == "" {
		return pythonNone
	}
	return value
}
