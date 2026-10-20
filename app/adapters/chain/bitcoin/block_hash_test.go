package bitcoin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBitcoin_BlockHashAt_RESTReadsTheHashOfTheHeight(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(esploraTestPrefix+"/block-height/154740", strings.ToUpper(esploraTestBlockHash)+"\n")

	hash, err := esplora.adapter(nil).BlockHashAt(context.Background(), esploraTestHeight)

	if err != nil || hash != esploraTestBlockHash {
		t.Fatalf("hash %q err %v, want %s", hash, err, esploraTestBlockHash)
	}
}

func TestBitcoin_BlockHashAt_RESTWithoutABlockIsAnError(t *testing.T) {
	cases := map[string]esploraAnswer{
		"height past the tip": {http.StatusNotFound, "Block not found"},
		"not a hash":          {http.StatusOK, "<html>maintenance</html>"},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			esplora := newFakeEsplora(t)
			esplora.on(esploraTestPrefix+"/block-height/154740", answer)

			hash, err := esplora.adapter(nil).BlockHashAt(context.Background(), esploraTestHeight)

			if err == nil || hash != "" {
				t.Fatalf("hash %q accepted, want an error", hash)
			}
		})
	}
}

func TestBitcoin_BlockHashAt_RPCAsksGetBlockHash(t *testing.T) {
	live := fakeBitcoind(t, map[string]string{
		"getblockhash": `{"jsonrpc":"2.0","id":1,"result":"` + esploraTestBlockHash + `"}`,
	})

	hash, err := live.BlockHashAt(context.Background(), esploraTestHeight)

	if err != nil || hash != esploraTestBlockHash {
		t.Fatalf("hash %q err %v", hash, err)
	}

	outOfRange := fakeBitcoind(t, map[string]string{
		"getblockhash": `{"jsonrpc":"2.0","id":1,"error":{"code":-8,"message":"Block height out of range"}}`,
	})
	if hash, err := outOfRange.BlockHashAt(context.Background(), esploraTestHeight); err == nil || hash != "" {
		t.Fatalf("hash %q accepted for a height the node does not have", hash)
	}
}

func TestElectrumProvider_BlockHashIsTheHashOfTheHeaderAtTheHeight(t *testing.T) {
	fake, rawURL := newTCPFakeElectrum(t)
	provider := ltcTestnetElectrum(t, rawURL)
	header := make([]byte, blockHeaderBytes)
	header[0], header[79] = 0x20, 0x7f
	first := sha256.Sum256(header)
	second := sha256.Sum256(first[:])
	for i, j := 0, len(second)-1; i < j; i, j = i+1, j-1 {
		second[i], second[j] = second[j], second[i]
	}
	want := hex.EncodeToString(second[:])
	fake.handle("blockchain.block.header", func(params []json.RawMessage) (any, *electrumError) {
		if string(params[0]) != "4907446" {
			return nil, &electrumError{Code: 1, Message: "height " + string(params[0]) + " out of range"}
		}
		return hex.EncodeToString(header), nil
	})

	hash, err := provider.blockHash(context.Background(), 4907446)
	if err != nil || hash != want {
		t.Fatalf("hash %q err %v, want %s", hash, err, want)
	}
	if hash, err := provider.blockHash(context.Background(), 4907447); err == nil || hash != "" {
		t.Fatalf("hash %q accepted for a height the server does not have", hash)
	}
}

func TestBitcoinFailover_BlockHashMovesToTheFallbackWhenThePrimaryIsDown(t *testing.T) {
	primary, primarySrv := newFailoverEsplora(t)
	primary.down.Store(true)
	fallbackHash := strings.Repeat("ab", 32)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/block-height/0":
			_, _ = w.Write([]byte(ltcTestnetGenesisHash))
		case "/block-height/4907446":
			_, _ = w.Write([]byte(fallbackHash))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fallback.Close)
	live := newFailoverTestLive(t, primarySrv.URL, fallback.URL)

	hash, err := live.BlockHashAt(context.Background(), 4907446)

	if err != nil || hash != fallbackHash {
		t.Fatalf("hash %q err %v, want the fallback's answer", hash, err)
	}
}
