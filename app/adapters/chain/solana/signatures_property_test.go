package solana

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/macrowallets/waas/app/services/chain"
)

// growingNode is an Agave-like node (a before or until it does not hold fails) whose
// history grows while a listing runs: after any getSignaturesForAddress call, new
// signatures may land, newer than everything, as between a page and its confirmation;
// a listing may then be served by a backend that does not show the newest one yet.
type growingNode struct {
	mu      sync.Mutex
	rng     *rand.Rand
	history []solanaSignatureInfo // newest first
	head    uint64
	n       int
	inject  float64
	served  bool
	// stale is how many listings (limit above 1) from the newest signature are still
	// served by a backend without the newest arrival.
	stale int
	// listedFromTheNewest counts the listings (limit above 1) without a before.
	listedFromTheNewest int
}

// arrive lands k signatures; one finalized after a head was served lands in a later
// slot, and several may share one.
func (r *growingNode) arrive(k int) {
	for i := 0; i < k; i++ {
		r.n++
		if r.served || r.rng.Intn(2) == 0 {
			r.head++
			r.served = false
		}
		r.history = append([]solanaSignatureInfo{{Signature: fmt.Sprintf("a-%06d", r.n), Slot: r.head}}, r.history...)
	}
}

func (r *growingNode) index(signature string) int {
	for i, entry := range r.history {
		if entry.Signature == signature {
			return i
		}
	}
	return -1
}

func (r *growingNode) answer(t *testing.T, req solanaRequest) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch req.Method {
	case "getSlot":
		r.served = true
		return fmt.Sprintf(`"result":%d`, r.head)
	case "getFirstAvailableBlock":
		return `"result":0`
	case "getSignaturesForAddress":
		var options struct {
			Before, Until string
			Limit         int
		}
		req.param(t, 1, &options)
		start := 0
		if options.Before != "" {
			at := r.index(options.Before)
			if at < 0 {
				return `"error":{"code":-32020,"message":"Transaction not found"}`
			}
			start = at + 1
		}
		if options.Until != "" && r.index(options.Until) < 0 {
			return `"error":{"code":-32020,"message":"Transaction not found"}`
		}
		visible := r.history
		if options.Limit > 1 && r.stale > 0 && start == 0 && len(visible) > 0 {
			r.stale--
			visible = visible[1:]
		}
		if options.Limit > 1 && options.Before == "" {
			r.listedFromTheNewest++
		}
		var page []solanaSignatureInfo
		for i := start; i < len(visible) && len(page) < options.Limit; i++ {
			if visible[i].Signature == options.Until {
				break
			}
			page = append(page, visible[i])
		}
		body, err := json.Marshal(page)
		if err != nil {
			t.Error(err)
		}
		if r.rng.Float64() < r.inject {
			r.arrive(1 + r.rng.Intn(3))
			if r.rng.Intn(3) == 0 {
				r.stale = 1 + r.rng.Intn(2)
			}
		}
		return `"result":` + string(body)
	}
	return `"error":{"code":-32601,"message":"Method not found"}`
}

// Over 2000 listings above a cursor on a node whose history grows between any two
// calls (the review of #130), every listing comes back as one contiguous run, and one
// that ends holds every signature between the cursor and the newest it returned, and
// every signature above that up to its head: reading the listing again after an empty
// page's confirmation missed never makes it skip a signature.
func TestSolana_AccountSignatures_NeverEndsAListingThatSkipsASignature(t *testing.T) {
	ended, unended, cut, relisted := 0, 0, 0, 0
	for seed := int64(1); seed <= 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		node := &growingNode{rng: rng, head: 1000, inject: []float64{0, 0.3, 0.7, 1}[rng.Intn(4)]}
		node.arrive(1 + rng.Intn(50)) // the cursor and older
		cursor := node.history[0]
		node.arrive([]int{0, 0, 1, 5, 999, 1000, 1001, 2500}[rng.Intn(8)]) // new above the cursor
		server := newMethodNode(t, func(req solanaRequest) string { return node.answer(t, req) })
		query := chain.SignatureQuery{Until: cursor.Signature, FloorSlot: cursor.Slot, HasFloor: true, MaxPages: 1 + rng.Intn(3)}

		got, err := liveOn(server.server.URL).AccountSignatures(context.Background(), ataFixtureOwner, query)

		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		node.mu.Lock()
		history := append([]solanaSignatureInfo(nil), node.history...)
		if node.listedFromTheNewest > 1 {
			relisted++
		}
		node.mu.Unlock()
		index := func(signature string) int {
			for i, entry := range history {
				if entry.Signature == signature {
					return i
				}
			}
			return -1
		}
		for i := 1; i < len(got.Signatures); i++ {
			if index(got.Signatures[i].Signature) != index(got.Signatures[i-1].Signature)+1 {
				t.Fatalf("seed %d: the listing is not one contiguous run", seed)
			}
		}
		cursorAt := index(cursor.Signature)
		switch {
		case got.More:
			cut++
		case got.Ended && !got.FloorCrossed:
			ended++
			newest := cursorAt
			if len(got.Signatures) > 0 {
				newest = index(got.Signatures[0].Signature)
				if oldest := index(got.Signatures[len(got.Signatures)-1].Signature); oldest+1 != cursorAt {
					t.Fatalf("seed %d: ended, but the oldest returned (#%d) is not right above the cursor (#%d)", seed, oldest, cursorAt)
				}
			}
			for i := 0; i < newest; i++ {
				if history[i].Slot <= got.Head {
					t.Fatalf("seed %d: ended, but %s at %d (head %d) above the newest returned was not listed", seed, history[i].Signature, history[i].Slot, got.Head)
				}
			}
		default:
			unended++
		}
		server.server.Close()
	}
	t.Logf("ended %d, unended %d, cut by MaxPages %d, listings read again %d", ended, unended, cut, relisted)
	if relisted == 0 {
		t.Fatal("no listing was read again: the run does not exercise the race")
	}
}
