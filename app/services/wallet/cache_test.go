package wallet

import (
	"context"
	"errors"
	"testing"
)

type recordingAddressCache struct {
	key     string
	members []any
	err     error
	calls   int
}

func (r *recordingAddressCache) SAdd(_ context.Context, key string, members ...any) error {
	r.calls++
	r.key = key
	r.members = append([]any(nil), members...)
	return r.err
}

func TestCache_Address_NilCacheDoesNothing(t *testing.T) {
	service := &Service{}
	service.cacheAddress(context.Background(), "eth", "0xabc")
}

func TestCache_Address_KeepsTheKeyAndTheAddress(t *testing.T) {
	cache := &recordingAddressCache{}
	service := &Service{addresses: cache}

	service.cacheAddress(context.Background(), "eth", "0xabc")

	if cache.calls != 1 {
		t.Fatalf("calls = %d", cache.calls)
	}
	if cache.key != "vault:addresses:eth" {
		t.Fatalf("key = %q", cache.key)
	}
	if len(cache.members) != 1 || cache.members[0] != "0xabc" {
		t.Fatalf("members = %v", cache.members)
	}
}

func TestCache_Address_LogsAndContinuesWhenRedisFails(t *testing.T) {
	cache := &recordingAddressCache{err: errors.New("boom")}
	service := &Service{addresses: cache}

	service.cacheAddress(context.Background(), "btc", "tb1qabc")

	if cache.calls != 1 {
		t.Fatalf("calls = %d", cache.calls)
	}
	if cache.key != "vault:addresses:btc" {
		t.Fatalf("key = %q", cache.key)
	}
}
