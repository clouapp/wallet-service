package chains

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
)

const setRPCFixture = "https://rpc.example/endpoint"

type setRPCStore struct {
	chain     *models.Chain
	findErr   error
	updateErr error
	id        string
	sealed    string
	finds     int
	updates   int
}

func (s *setRPCStore) FindByID(_ context.Context, id string) (*models.Chain, error) {
	s.finds++
	s.id = id
	if s.findErr != nil {
		return nil, s.findErr
	}
	return s.chain, nil
}

func (s *setRPCStore) UpdateRPCURL(_ context.Context, id, sealed string) error {
	s.updates++
	s.id = id
	s.sealed = sealed
	return s.updateErr
}

func TestReplace_RPC_RejectsBlankArgumentsBeforeAnyQuery(t *testing.T) {
	store := &setRPCStore{}
	rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store, Seal: func(string) (string, error) { return "sealed", nil }})
	if _, err := rpc.Replace(context.Background(), "  ", setRPCFixture, false); err == nil || err.Error() != "chain_id is required" {
		t.Fatalf("chain id error = %v", err)
	}
	if _, err := rpc.Replace(context.Background(), "eth", "  ", false); err == nil || err.Error() != "url is required" {
		t.Fatalf("url error = %v", err)
	}
	if store.finds != 0 || store.updates != 0 {
		t.Fatal("store was called for a blank argument")
	}
}

func TestReplace_RPC_RejectsABadEndpointBeforeAnyQuery(t *testing.T) {
	store := &setRPCStore{}
	rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store})
	cases := []string{
		"env:not-a-name",
		"ftp://rpc.example",
	}
	for _, raw := range cases {
		_, err := rpc.Replace(context.Background(), "eth", raw, false)
		if err == nil || strings.Contains(err.Error(), setRPCFixture) || strings.Contains(err.Error(), "not-a-name") && strings.Contains(err.Error(), "ftp") {
			t.Fatalf("error = %v", err)
		}
		if strings.Contains(err.Error(), raw) && strings.Contains(raw, "ftp") {
			t.Fatalf("error included the endpoint: %v", err)
		}
	}
	if _, err := rpc.Replace(context.Background(), "eth", "env:not-a-name", false); err == nil || err.Error() != "env reference must look like env:SOLANA_RPC_URL" {
		t.Fatal("env reference was accepted")
	}
	if _, err := rpc.Replace(context.Background(), "eth", "ftp://rpc.example", false); err == nil || !strings.Contains(err.Error(), "http://") {
		t.Fatal("bad scheme was accepted")
	}
	if store.finds != 0 || store.updates != 0 {
		t.Fatal("store was called for a rejected endpoint")
	}
}

func TestReplace_RPC_ReportsAMissingChain(t *testing.T) {
	store := &setRPCStore{findErr: models.ErrRepositoryNotFound}
	_, err := NewReplaceRPC(ReplaceRPCDeps{Store: store}).Replace(context.Background(), " eth ", setRPCFixture, false)
	if err == nil || err.Error() != "chain not found: eth" || strings.Contains(err.Error(), setRPCFixture) {
		t.Fatalf("error = %v", err)
	}
	if store.finds != 1 || store.updates != 0 || store.id != "eth" {
		t.Fatalf("finds=%d updates=%d id=%s", store.finds, store.updates, store.id)
	}
}

func TestReplace_RPC_ReportsAnEmptyChainRowAsMissing(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{}}
	_, err := NewReplaceRPC(ReplaceRPCDeps{Store: store}).Replace(context.Background(), "eth", setRPCFixture, false)
	if err == nil || err.Error() != "chain not found: eth" || store.updates != 0 {
		t.Fatalf("error = %v updates=%d", err, store.updates)
	}
}

func TestReplace_RPC_ReportsALoadFailure(t *testing.T) {
	inner := errors.New("connection refused")
	store := &setRPCStore{findErr: fmtWrap("find chain", inner)}
	_, err := NewReplaceRPC(ReplaceRPCDeps{Store: store}).Replace(context.Background(), "eth", setRPCFixture, false)
	if err == nil || err.Error() != "failed to load chain eth: connection refused" || !errors.Is(err, inner) {
		t.Fatalf("error = %v", err)
	}
	if store.updates != 0 || strings.Contains(err.Error(), setRPCFixture) {
		t.Fatal("load failure wrote or echoed the endpoint")
	}
}

func TestReplace_RPC_WritesTheSealedEndpoint(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{ID: "eth"}}
	rpc := NewReplaceRPC(ReplaceRPCDeps{
		Store:  store,
		Verify: &stubVerifier{network: models.NetworkEthereumSepolia},
		Seal: func(plaintext string) (string, error) {
			if plaintext != setRPCFixture {
				t.Fatal("sealer received a different value than the argument")
			}
			return "enc:v1:sealed", nil
		},
	})
	line, err := rpc.Replace(context.Background(), "eth", "  "+setRPCFixture+"  ", false)
	if err != nil {
		t.Fatal(err)
	}
	if store.updates != 1 || store.id != "eth" || store.sealed != "enc:v1:sealed" || store.sealed == setRPCFixture {
		t.Fatalf("update id=%s sealed=%s updates=%d", store.id, store.sealed, store.updates)
	}
	if strings.Contains(line, setRPCFixture) || strings.Contains(line, "enc:v1:") {
		t.Fatal("success output included the endpoint")
	}
}

func TestReplace_RPC_SealsAnEnvironmentReference(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{ID: "sol"}}
	rpc := NewReplaceRPC(ReplaceRPCDeps{
		Store:  store,
		Verify: &stubVerifier{network: models.NetworkSolanaDevnet},
		Seal: func(plaintext string) (string, error) {
			if plaintext != "env:SOLANA_RPC_URL" {
				t.Fatal("environment reference was rewritten before sealing")
			}
			return "enc:v1:sealed", nil
		},
	})
	line, err := rpc.Replace(context.Background(), "sol", "env:SOLANA_RPC_URL", false)
	if err != nil || store.sealed != "enc:v1:sealed" {
		t.Fatalf("err=%v sealed=%s", err, store.sealed)
	}
	if strings.Contains(line, "SOLANA_RPC_URL") || strings.Contains(line, "enc:v1:") {
		t.Fatal("output included the reference or the sealed value")
	}
}

func TestReplace_RPC_ReportsAnEncryptionFailure(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{ID: "eth"}}
	rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store, Verify: &stubVerifier{}, Seal: func(string) (string, error) {
		return "", errors.New("cipher unavailable")
	}})
	_, err := rpc.Replace(context.Background(), "eth", setRPCFixture, false)
	if err == nil || err.Error() != "failed to encrypt url: cipher unavailable" || store.updates != 0 {
		t.Fatalf("error = %v updates=%d", err, store.updates)
	}
	if strings.Contains(err.Error(), setRPCFixture) {
		t.Fatal("encryption failure included the endpoint")
	}
}

func TestReplace_RPC_ReportsNoRowsUpdated(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{ID: "eth"}, updateErr: models.ErrRepositoryNotFound}
	rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store, Verify: &stubVerifier{}, Seal: func(string) (string, error) { return "enc:v1:sealed", nil }})
	_, err := rpc.Replace(context.Background(), "eth", setRPCFixture, false)
	if err == nil || err.Error() != "no rows updated for chain eth" {
		t.Fatalf("error = %v", err)
	}
}

func TestReplace_RPC_ReportsAnUpdateFailure(t *testing.T) {
	inner := errors.New("disk full")
	store := &setRPCStore{chain: &models.Chain{ID: "eth"}, updateErr: fmtWrap("update chain rpc", inner)}
	rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store, Verify: &stubVerifier{}, Seal: func(string) (string, error) { return "enc:v1:sealed", nil }})
	_, err := rpc.Replace(context.Background(), "eth", setRPCFixture, false)
	if err == nil || err.Error() != "failed to update chain eth: disk full" || !errors.Is(err, inner) {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), setRPCFixture) || strings.Contains(err.Error(), "enc:v1:") {
		t.Fatal("update failure included the endpoint")
	}
}

func TestReplace_RPC_RequiresTheRepository(t *testing.T) {
	_, err := NewReplaceRPC(ReplaceRPCDeps{}).Replace(context.Background(), "eth", setRPCFixture, false)
	if err == nil || err.Error() != "chain repository is not configured" || strings.Contains(err.Error(), setRPCFixture) {
		t.Fatalf("error = %v", err)
	}
}

type wrappedError struct {
	message string
	cause   error
}

func (e wrappedError) Error() string { return e.message + ": " + e.cause.Error() }
func (e wrappedError) Unwrap() error { return e.cause }

func fmtWrap(message string, cause error) error {
	return wrappedError{message: message, cause: cause}
}

// stubVerifier answers every endpoint with the same network check.
type stubVerifier struct {
	network string
	moved   bool
	err     error
	asked   int
}

func (v *stubVerifier) Verify(context.Context, models.Chain, string) (string, bool, error) {
	v.asked++
	return v.network, v.moved, v.err
}

func TestReplace_RPC_RefusesAnEndpointOnAnotherNetworkOrThatDoesNotAnswer(t *testing.T) {
	cases := map[string]error{
		"another network": fmt.Errorf("%w: eth_chainId is 1, the chain signs for 11155111", chain.ErrWrongNetwork),
		"no answer":       fmt.Errorf("%w: eth_chainId: provider unavailable", chainregistry.ErrEndpointUnverified),
	}
	for name, failure := range cases {
		t.Run(name, func(t *testing.T) {
			store := &setRPCStore{chain: &models.Chain{ID: "eth", AdapterType: models.AdapterTypeEVM}}
			sealed := false
			rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store, Verify: &stubVerifier{err: failure}, Seal: func(string) (string, error) {
				sealed = true
				return "enc:v1:sealed", nil
			}})
			_, err := rpc.Replace(context.Background(), "eth", "http://5.104.72.66:8545", false)
			if err == nil || !strings.HasPrefix(err.Error(), "refused, nothing written") || strings.Contains(err.Error(), "5.104.72.66") {
				t.Fatalf("error = %v", err)
			}
			if store.updates != 0 || sealed {
				t.Fatal("a refused endpoint was sealed or written")
			}
		})
	}
}

func TestReplace_RPC_SaysWhichNetworkTheEndpointServes(t *testing.T) {
	cases := map[string]struct {
		verifier *stubVerifier
		want     string
	}{
		"own network": {&stubVerifier{network: models.NetworkEthereumSepolia}, "chain eth rpc_url updated (serves ethereum-sepolia; restart"},
		"profile move": {&stubVerifier{network: models.NetworkEthereumSepolia, moved: true},
			"serves ethereum-sepolia, the network the chain profile names for eth: run chains:align-network --apply to move the chain"},
		"unchecked": {&stubVerifier{err: chainregistry.ErrEndpointIdentityUnsupported}, "network not checked"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &setRPCStore{chain: &models.Chain{ID: "eth"}}
			rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store, Verify: tc.verifier, Seal: func(string) (string, error) { return "enc:v1:sealed", nil }})
			line, err := rpc.Replace(context.Background(), "eth", setRPCFixture, false)
			if err != nil || !strings.Contains(line, tc.want) || store.updates != 1 {
				t.Fatalf("line=%q err=%v updates=%d", line, err, store.updates)
			}
		})
	}
}

func TestReplace_RPC_RequiresTheVerifierBeforeWriting(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{ID: "eth"}}
	_, err := NewReplaceRPC(ReplaceRPCDeps{Store: store, Seal: func(string) (string, error) { return "enc:v1:sealed", nil }}).Replace(context.Background(), "eth", setRPCFixture, false)
	if err == nil || store.updates != 0 {
		t.Fatalf("err=%v updates=%d", err, store.updates)
	}
}

func TestReplace_RPC_SkipsTheNetworkCheckOnlyWhenAskedAndSaysSoLoudly(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{ID: "eth", AdapterType: models.AdapterTypeEVM}}
	verifier := &stubVerifier{err: fmt.Errorf("%w: eth_chainId: provider unavailable", chainregistry.ErrEndpointUnverified)}
	rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store, Verify: verifier, Seal: func(string) (string, error) { return "enc:v1:sealed", nil }})

	if _, err := rpc.Replace(context.Background(), "eth", "http://10.0.0.5:8545", false); err == nil || store.updates != 0 {
		t.Fatalf("without the flag an endpoint that does not answer is refused: err=%v updates=%d", err, store.updates)
	}
	line, err := rpc.Replace(context.Background(), "eth", "http://10.0.0.5:8545", true)
	if err != nil || store.updates != 1 {
		t.Fatalf("err=%v updates=%d", err, store.updates)
	}
	if verifier.asked != 1 {
		t.Fatalf("the endpoint was asked %d times; --skip-network-check asks nothing", verifier.asked)
	}
	if !strings.Contains(line, "WITHOUT a network check (--skip-network-check)") || strings.Contains(line, "10.0.0.5") {
		t.Fatalf("line = %q", line)
	}
}
