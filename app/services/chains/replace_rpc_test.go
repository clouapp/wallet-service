package chains

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
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

func TestReplaceRPCRejectsBlankArgumentsBeforeAnyQuery(t *testing.T) {
	store := &setRPCStore{}
	rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store, Seal: func(string) (string, error) { return "sealed", nil }})
	if _, err := rpc.Replace(context.Background(), "  ", setRPCFixture); err == nil || err.Error() != "chain_id is required" {
		t.Fatalf("chain id error = %v", err)
	}
	if _, err := rpc.Replace(context.Background(), "eth", "  "); err == nil || err.Error() != "url is required" {
		t.Fatalf("url error = %v", err)
	}
	if store.finds != 0 || store.updates != 0 {
		t.Fatal("store was called for a blank argument")
	}
}

func TestReplaceRPCRejectsABadEndpointBeforeAnyQuery(t *testing.T) {
	store := &setRPCStore{}
	rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store})
	cases := []string{
		"env:not-a-name",
		"ftp://rpc.example",
	}
	for _, raw := range cases {
		_, err := rpc.Replace(context.Background(), "eth", raw)
		if err == nil || strings.Contains(err.Error(), setRPCFixture) || strings.Contains(err.Error(), "not-a-name") && strings.Contains(err.Error(), "ftp") {
			t.Fatalf("error = %v", err)
		}
		if strings.Contains(err.Error(), raw) && strings.Contains(raw, "ftp") {
			t.Fatalf("error included the endpoint: %v", err)
		}
	}
	if _, err := rpc.Replace(context.Background(), "eth", "env:not-a-name"); err == nil || err.Error() != "env reference must look like env:SOLANA_RPC_URL" {
		t.Fatal("env reference was accepted")
	}
	if _, err := rpc.Replace(context.Background(), "eth", "ftp://rpc.example"); err == nil || !strings.Contains(err.Error(), "http://") {
		t.Fatal("bad scheme was accepted")
	}
	if store.finds != 0 || store.updates != 0 {
		t.Fatal("store was called for a rejected endpoint")
	}
}

func TestReplaceRPCReportsAMissingChain(t *testing.T) {
	store := &setRPCStore{findErr: models.ErrRepositoryNotFound}
	_, err := NewReplaceRPC(ReplaceRPCDeps{Store: store}).Replace(context.Background(), " eth ", setRPCFixture)
	if err == nil || err.Error() != "chain not found: eth" || strings.Contains(err.Error(), setRPCFixture) {
		t.Fatalf("error = %v", err)
	}
	if store.finds != 1 || store.updates != 0 || store.id != "eth" {
		t.Fatalf("finds=%d updates=%d id=%s", store.finds, store.updates, store.id)
	}
}

func TestReplaceRPCReportsAnEmptyChainRowAsMissing(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{}}
	_, err := NewReplaceRPC(ReplaceRPCDeps{Store: store}).Replace(context.Background(), "eth", setRPCFixture)
	if err == nil || err.Error() != "chain not found: eth" || store.updates != 0 {
		t.Fatalf("error = %v updates=%d", err, store.updates)
	}
}

func TestReplaceRPCReportsALoadFailure(t *testing.T) {
	inner := errors.New("connection refused")
	store := &setRPCStore{findErr: fmtWrap("find chain", inner)}
	_, err := NewReplaceRPC(ReplaceRPCDeps{Store: store}).Replace(context.Background(), "eth", setRPCFixture)
	if err == nil || err.Error() != "failed to load chain eth: connection refused" || !errors.Is(err, inner) {
		t.Fatalf("error = %v", err)
	}
	if store.updates != 0 || strings.Contains(err.Error(), setRPCFixture) {
		t.Fatal("load failure wrote or echoed the endpoint")
	}
}

func TestReplaceRPCWritesTheSealedEndpoint(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{ID: "eth"}}
	rpc := NewReplaceRPC(ReplaceRPCDeps{
		Store: store,
		Seal: func(plaintext string) (string, error) {
			if plaintext != setRPCFixture {
				t.Fatal("sealer received a different value than the argument")
			}
			return "enc:v1:sealed", nil
		},
	})
	line, err := rpc.Replace(context.Background(), "eth", "  "+setRPCFixture+"  ")
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

func TestReplaceRPCSealsAnEnvironmentReference(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{ID: "sol"}}
	rpc := NewReplaceRPC(ReplaceRPCDeps{
		Store: store,
		Seal: func(plaintext string) (string, error) {
			if plaintext != "env:SOLANA_RPC_URL" {
				t.Fatal("environment reference was rewritten before sealing")
			}
			return "enc:v1:sealed", nil
		},
	})
	line, err := rpc.Replace(context.Background(), "sol", "env:SOLANA_RPC_URL")
	if err != nil || store.sealed != "enc:v1:sealed" {
		t.Fatalf("err=%v sealed=%s", err, store.sealed)
	}
	if strings.Contains(line, "SOLANA_RPC_URL") || strings.Contains(line, "enc:v1:") {
		t.Fatal("output included the reference or the sealed value")
	}
}

func TestReplaceRPCReportsAnEncryptionFailure(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{ID: "eth"}}
	rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store, Seal: func(string) (string, error) {
		return "", errors.New("cipher unavailable")
	}})
	_, err := rpc.Replace(context.Background(), "eth", setRPCFixture)
	if err == nil || err.Error() != "failed to encrypt url: cipher unavailable" || store.updates != 0 {
		t.Fatalf("error = %v updates=%d", err, store.updates)
	}
	if strings.Contains(err.Error(), setRPCFixture) {
		t.Fatal("encryption failure included the endpoint")
	}
}

func TestReplaceRPCReportsNoRowsUpdated(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{ID: "eth"}, updateErr: models.ErrRepositoryNotFound}
	rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store, Seal: func(string) (string, error) { return "enc:v1:sealed", nil }})
	_, err := rpc.Replace(context.Background(), "eth", setRPCFixture)
	if err == nil || err.Error() != "no rows updated for chain eth" {
		t.Fatalf("error = %v", err)
	}
}

func TestReplaceRPCReportsAnUpdateFailure(t *testing.T) {
	inner := errors.New("disk full")
	store := &setRPCStore{chain: &models.Chain{ID: "eth"}, updateErr: fmtWrap("update chain rpc", inner)}
	rpc := NewReplaceRPC(ReplaceRPCDeps{Store: store, Seal: func(string) (string, error) { return "enc:v1:sealed", nil }})
	_, err := rpc.Replace(context.Background(), "eth", setRPCFixture)
	if err == nil || err.Error() != "failed to update chain eth: disk full" || !errors.Is(err, inner) {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), setRPCFixture) || strings.Contains(err.Error(), "enc:v1:") {
		t.Fatal("update failure included the endpoint")
	}
}

func TestReplaceRPCRequiresTheRepository(t *testing.T) {
	_, err := NewReplaceRPC(ReplaceRPCDeps{}).Replace(context.Background(), "eth", setRPCFixture)
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
