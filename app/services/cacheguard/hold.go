package cacheguard

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	contractscache "github.com/goravel/framework/contracts/cache"
)

// Hold is a lock taken under an owner token and renewed while it is held: every
// third of its ttl the holder puts its token back for another ttl, so a holder
// that runs longer than the ttl keeps it, and one that died frees it within the
// ttl. Release forgets the key only while it still holds this holder's token.
//
// The token checks and the writes are two cache calls (the driver has no compare
// and set). The renewal keeps the key from lapsing while the holder runs, so
// another holder can take it only after this one stopped renewing; a renewal or a
// release that finds another token leaves it alone.
type Hold struct {
	cache contractscache.Driver
	key   string
	token string
	ttl   time.Duration

	once sync.Once
	stop chan struct{}
	done chan struct{}
}

// AcquireHold takes key for ttl when it is absent, like Acquire, and renews it until
// Release. A held key is (nil, false, nil); a cache that refused the write is
// ErrUnavailable.
func AcquireHold(c contractscache.Driver, key string, ttl time.Duration) (*Hold, bool, error) {
	if ttl <= 0 {
		return nil, false, fmt.Errorf("acquire %s: a positive ttl is required", key)
	}
	token, err := newOwnerToken()
	if err != nil {
		return nil, false, fmt.Errorf("acquire %s: %w", key, err)
	}
	if !c.Add(key, token, ttl) {
		if c.Has(key) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("acquire %s: %w", key, ErrUnavailable)
	}
	hold := &Hold{cache: c, key: key, token: token, ttl: ttl, stop: make(chan struct{}), done: make(chan struct{})}
	go hold.renew()
	return hold, true, nil
}

// Release stops the renewal and forgets the key while it holds this token. A
// second call does nothing.
func (h *Hold) Release() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		close(h.stop)
		<-h.done
		if h.owned() {
			h.cache.Forget(h.key)
		}
	})
}

func (h *Hold) renew() {
	defer close(h.done)
	ticker := time.NewTicker(h.ttl / 3)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			if !h.owned() {
				return
			}
			_ = h.cache.Put(h.key, h.token, h.ttl)
		}
	}
}

func (h *Hold) owned() bool {
	return h.cache.GetString(h.key) == h.token
}

func newOwnerToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
