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
// release that finds another token leaves it alone. A renewal that finds the key
// gone or another token, or that cannot write, has lost the hold: Lost is closed,
// so the holder can stop before it acts on what it no longer holds.
type Hold struct {
	cache contractscache.Driver
	key   string
	token string
	ttl   time.Duration

	once     sync.Once
	stop     chan struct{}
	done     chan struct{}
	lostOnce sync.Once
	lost     chan struct{}
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
	hold := &Hold{cache: c, key: key, token: token, ttl: ttl, stop: make(chan struct{}), done: make(chan struct{}), lost: make(chan struct{})}
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

// Lost is closed once the hold is known lost: a renewal found the key gone or
// another token, or could not write it, or Held found it so. It stays open while the
// hold is kept and after a Release.
func (h *Hold) Lost() <-chan struct{} {
	return h.lost
}

// Held reports whether the key still holds this holder's token now; a hold found
// lost closes Lost. A cache that cannot be read reads as lost.
func (h *Hold) Held() bool {
	if h.owned() {
		return true
	}
	h.markLost()
	return false
}

func (h *Hold) markLost() {
	h.lostOnce.Do(func() { close(h.lost) })
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
				h.markLost()
				return
			}
			if err := h.cache.Put(h.key, h.token, h.ttl); err != nil {
				h.markLost()
				return
			}
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
