//go:build generate_preparams

package tssfixture

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
)

// TestGenerate_PreParams rewrites testdata/preparams.json with fresh sets.
func TestGenerate_PreParams(t *testing.T) {
	const count = 4
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	out := make([]keygen.LocalPreParams, 0, count)
	for range count {
		params, err := keygen.GeneratePreParamsWithContext(ctx, runtime.NumCPU())
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if !params.ValidateWithProof() {
			t.Fatal("generated pre-params do not validate")
		}
		out = append(out, *params)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.WriteFile("testdata/preparams.json", data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
