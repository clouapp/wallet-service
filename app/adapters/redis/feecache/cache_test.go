package feecache

import (
	"context"
	"testing"
	"time"
)

func TestNil_Client_IsANoOp(t *testing.T) {
	t.Parallel()

	cache := New(nil)
	if _, ok, err := cache.Get(context.Background(), "k"); ok || err != nil {
		t.Fatalf("ok %t err %v", ok, err)
	}
	if err := cache.Set(context.Background(), "k", []byte("v"), time.Second); err != nil {
		t.Fatal(err)
	}
}
