package tron

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestTron_BlockHashAt_ReadsTheHeaderOfTheHeight(t *testing.T) {
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

	hash, err := adapter.BlockHashAt(context.Background(), uint64(tronTestSolidNumber))

	if err != nil || hash != tronTestSolidBlockID {
		t.Fatalf("hash %q err %v, want the block id", hash, err)
	}
	if len(paths) != 1 || paths[0] != "/wallet/getblock" {
		t.Fatalf("paths %v, want getblock only", paths)
	}
	if body["id_or_num"] != "71731253" || body["detail"] != false {
		t.Fatalf("body %v, want the height without transactions", body)
	}
}

func TestTron_BlockHashAt_FallsBackToGetBlockByNumOnOlderNodes(t *testing.T) {
	var paths []string
	adapter := newTronHTTPAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/wallet/getblock" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, strings.TrimSuffix(tronTestSolidHeader, "}")+`,"transactions":[]}`)
	}, "")

	hash, err := adapter.BlockHashAt(context.Background(), uint64(tronTestSolidNumber))

	if err != nil || hash != tronTestSolidBlockID {
		t.Fatalf("hash %q err %v", hash, err)
	}
	if len(paths) != 2 || paths[1] != "/wallet/getblockbynum" {
		t.Fatalf("paths %v, want getblock then getblockbynum", paths)
	}
}

func TestTron_BlockHashAt_NoBlockAtTheHeightIsAnError(t *testing.T) {
	cases := map[string]string{
		"empty answer (past the head)": `{}`,
		"another height":               tronTestSolidHeader,
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			adapter := newTronHTTPAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, answer)
			}, "")

			hash, err := adapter.BlockHashAt(context.Background(), uint64(tronTestSolidNumber)+1)

			if err == nil || hash != "" {
				t.Fatalf("hash %q accepted, want an error", hash)
			}
		})
	}
}
