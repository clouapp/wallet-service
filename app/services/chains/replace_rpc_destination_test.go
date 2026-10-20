package chains

import (
	"context"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/egress"
)

func TestReplace_RPC_RefusesALinkLocalOrMetadataEndpointEvenWithoutANetworkCheck(t *testing.T) {
	for url, reason := range map[string]string{
		"http://169.254.169.254/latest/meta-data/": egress.ReasonLinkLocal,
		"http://100.100.100.200/latest/":           egress.ReasonLinkLocal,
		"http://metadata.google.internal/":         egress.ReasonMetadata,
	} {
		for _, skip := range []bool{false, true} {
			store := &setRPCStore{chain: &models.Chain{ID: "eth"}}
			verifier := &stubVerifier{network: models.NetworkEthereumSepolia}
			rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store, Verify: verifier, Fallbacks: &recordingFallbacks{},
				Seal: func(string) (string, error) { return "enc:v1:sealed", nil }})

			_, err := rpc.Replace(context.Background(), "eth", url, skip)
			if err == nil || !strings.Contains(err.Error(), reason) || strings.Contains(err.Error(), url) {
				t.Fatalf("%s (skip %v): err = %v, want the refusal without the url", url, skip, err)
			}
			if verifier.asked != 0 || store.updates != 0 || store.finds != 0 {
				t.Fatalf("%s (skip %v): asked %d, wrote %d, read %d", url, skip, verifier.asked, store.updates, store.finds)
			}
		}
	}
}

func TestReplace_RPC_AcceptsALinkLocalEndpointWithTheFlag(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{ID: "eth"}}
	rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store, Verify: &stubVerifier{network: models.NetworkEthereumSepolia},
		Seal: func(string) (string, error) { return "enc:v1:sealed", nil }, AllowLinkLocal: true})
	if _, err := rpc.Replace(context.Background(), "eth", "http://169.254.10.20:8545", false); err != nil {
		t.Fatal(err)
	}
	if store.updates != 1 {
		t.Fatalf("updates = %d", store.updates)
	}
}
