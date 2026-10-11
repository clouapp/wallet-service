package mpc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"runtime"
	"sync"
	"testing"

	"github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
)

// pooledKeygen runs one secp256k1 keygen on a pool of real pre-parameters, once per
// test binary (the safe primes take seconds), and returns it with the NTilde of every
// set the pool generated.
var pooledKeygen = sync.OnceValues(func() (*pooledKeygenResult, error) {
	var mu sync.Mutex
	var generated []*big.Int
	pool := NewPreParamsPool(2, func(ctx context.Context) (*keygen.LocalPreParams, error) {
		params, err := keygen.GeneratePreParamsWithContext(ctx, runtime.NumCPU())
		if err == nil {
			mu.Lock()
			generated = append(generated, params.NTildei)
			mu.Unlock()
		}
		return params, err
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pool.Run(ctx)
	for !pool.running.Load() {
		runtime.Gosched()
	}

	svc := NewTSSServiceWithPreParams(pool)
	keys, err := svc.Keygen(context.Background(), CurveSecp256k1)
	if err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	return &pooledKeygenResult{svc: svc, keys: keys, generated: append([]*big.Int(nil), generated...)}, nil
})

type pooledKeygenResult struct {
	svc       *TSSService
	keys      *KeygenResult
	generated []*big.Int
}

func TestKeygen_Secp256k1_UsesThePooledPreParams(t *testing.T) {
	result, err := pooledKeygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	used := map[string]bool{}
	for name, share := range map[string][]byte{"A": result.keys.ShareA, "B": result.keys.ShareB} {
		var save keygen.LocalPartySaveData
		if err := json.Unmarshal(share, &save); err != nil {
			t.Fatalf("share %s: %v", name, err)
		}
		fromPool := false
		for _, n := range result.generated {
			if save.NTildei != nil && save.NTildei.Cmp(n) == 0 {
				fromPool = true
			}
		}
		if !fromPool {
			t.Fatalf("share %s was not built on pooled pre-params", name)
		}
		used[save.NTildei.String()] = true
	}
	if len(used) != 2 {
		t.Fatal("both parties used the same pre-params")
	}
}

func TestKeygen_Secp256k1_PooledKeysSignForTheWalletPublicKey(t *testing.T) {
	result, err := pooledKeygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	digest := sha256.Sum256([]byte("secp256k1 pooled pre-params"))
	signature, err := result.svc.Sign(context.Background(), CurveSecp256k1, result.keys.ShareA, result.keys.ShareB, SignInputs{
		TxHashes:          [][]byte{digest[:]},
		ExpectedPublicKey: result.keys.CombinedPubKey,
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !verifyDER(t, signature, digest[:], result.keys.CombinedPubKey) {
		t.Fatal("signature does not verify against the wallet public key")
	}
}
