package tron

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

// Nile's solidified block on 2026-10-11, 18 blocks behind the head.
const (
	tronTestSolidNumber  = int64(71731253)
	tronTestSolidBlockID = "00000000044688350c233fd9ff061b73bd0a3a5970a24772ad93374f3e78540d"
	tronTestSolidHeader  = `{"blockID":"` + tronTestSolidBlockID + `","block_header":{"raw_data":{"number":71731253,"timestamp":1791724320000}}}`
)

func TestTron_SolidifiedBlock_ReadsTheSolidityNodeHeader(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var body map[string]any
	adapter := newTronHTTPAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = io.WriteString(w, tronTestSolidHeader)
	}, "")

	block, err := adapter.SolidifiedBlock(context.Background())
	if err != nil || block != uint64(tronTestSolidNumber) {
		t.Fatalf("solidified block %d, %v", block, err)
	}
	if len(paths) != 1 || paths[0] != "/walletsolidity/getblock" {
		t.Fatalf("paths %v, want the solidity getblock only", paths)
	}
	if detail, ok := body["detail"].(bool); !ok || detail {
		t.Fatalf("body %v asks for the transactions", body)
	}
}

func TestTron_SolidifiedBlock_FallsBackToGetNowBlockOnOlderNodes(t *testing.T) {
	var paths []string
	adapter := newTronHTTPAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/walletsolidity/getblock" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, strings.TrimSuffix(tronTestSolidHeader, "}")+`,"transactions":[]}`)
	}, "")

	block, err := adapter.SolidifiedBlock(context.Background())
	if err != nil || block != uint64(tronTestSolidNumber) {
		t.Fatalf("solidified block %d, %v", block, err)
	}
	if strings.Join(paths, ",") != "/walletsolidity/getblock,/walletsolidity/getnowblock" {
		t.Fatalf("paths %v", paths)
	}
}

func TestTron_SolidifiedBlock_FailsWithoutAValidHeader(t *testing.T) {
	cases := map[string]func(w http.ResponseWriter, r *http.Request){
		"no solidity api": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"node error": func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"Error":"class org.tron.core.exception.ItemNotFoundException"}`)
		},
		"empty block": func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{}`) },
		"id of another block": func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"blockID":"`+tronTestHeadBlockID+`","block_header":{"raw_data":{"number":71731253,"timestamp":1791724320000}}}`)
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			adapter := newTronHTTPAdapter(t, handler, "")
			if block, err := adapter.SolidifiedBlock(context.Background()); err == nil {
				t.Fatalf("solidified block %d accepted", block)
			}
		})
	}
}

func TestTron_FinalityPolicy_IsTheChainRowPolicy(t *testing.T) {
	adapter := NewTronLive(TronConfig{ChainIDStr: models.ChainTTron, Finality: models.FinalitySolidified})
	var reporter chain.FinalityPolicyReporter = adapter
	var reader chain.SolidifiedBlockReader = adapter
	if reporter.FinalityPolicy() != models.FinalitySolidified || reader == nil {
		t.Fatalf("policy %q", reporter.FinalityPolicy())
	}
	if got := chain.FinalityPolicyOf(NewTronLive(TronConfig{ChainIDStr: models.ChainTron})); got != models.FinalityConfirmations {
		t.Fatalf("a row without a policy reports %q", got)
	}
}
