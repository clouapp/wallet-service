package addressset

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/services/ingest"
)

// Set reads watched addresses with SISMEMBER. The service keeps the key and the decision.
type Set struct {
	client *redis.Client
}

// New wraps client. A nil client returns a nil set so ingest falls back to the address store.
func New(client *redis.Client) ingest.AddressSet {
	if client == nil {
		return nil
	}
	return &Set{client: client}
}

// SIsMember reports whether member is in key. A blank key or a nil context fails before Redis.
func (s *Set) SIsMember(ctx context.Context, key string, member any) ingest.Membership {
	if s == nil || s.client == nil {
		return rejectedMembership{err: fmt.Errorf("redis address set: client is nil")}
	}
	if ctx == nil {
		return rejectedMembership{err: fmt.Errorf("redis address set: context is nil")}
	}
	if strings.TrimSpace(key) == "" {
		return rejectedMembership{err: fmt.Errorf("redis address set: key is required")}
	}
	return s.client.SIsMember(ctx, key, member)
}

type rejectedMembership struct {
	err error
}

func (r rejectedMembership) Result() (bool, error) {
	if r.err == nil {
		return false, fmt.Errorf("redis address set: missing error")
	}
	return false, r.err
}
