package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

const setRPCFixture = "https://rpc.example/endpoint"

type setRPCOutput struct {
	errors []string
	infos  []string
}

func (o *setRPCOutput) Error(message string) { o.errors = append(o.errors, message) }
func (o *setRPCOutput) Info(message string)  { o.infos = append(o.infos, message) }

func (o *setRPCOutput) text() string {
	return strings.Join(append(append([]string{}, o.errors...), o.infos...), "\n")
}

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

func TestChainsSetRPCSignature(t *testing.T) {
	cmd := NewChainsSetRPC(nil)
	if cmd.Signature() != "chains:set-rpc" {
		t.Fatalf("signature = %s", cmd.Signature())
	}
	if cmd.Description() == "" {
		t.Fatal("empty description")
	}
	ext := cmd.Extend()
	if ext.Category != "chains" {
		t.Fatalf("category = %s", ext.Category)
	}
	if len(ext.Arguments) != 2 {
		t.Fatalf("arguments = %d", len(ext.Arguments))
	}
}

func TestChainsSetRPCRejectsBlankArgumentsBeforeAnyQuery(t *testing.T) {
	store := &setRPCStore{}
	cmd := NewChainsSetRPC(store)
	out := &setRPCOutput{}
	if err := cmd.setRPC(out, "  ", setRPCFixture); err == nil || err.Error() != "chain_id is required" {
		t.Fatalf("chain id error = %v", err)
	}
	if store.finds != 0 || store.updates != 0 {
		t.Fatalf("store was called for a blank chain id: finds=%d updates=%d", store.finds, store.updates)
	}
	out = &setRPCOutput{}
	if err := cmd.setRPC(out, "eth", "  "); err == nil || err.Error() != "url is required" {
		t.Fatalf("url error = %v", err)
	}
	if store.finds != 0 || store.updates != 0 {
		t.Fatal("store was called for a blank url")
	}
}

func TestChainsSetRPCRejectsABadEndpointBeforeAnyQuery(t *testing.T) {
	store := &setRPCStore{}
	cmd := NewChainsSetRPC(store)
	cases := []struct {
		url     string
		printed string
		err     string
	}{
		{"env:not-a-name", "env reference must look like env:SOLANA_RPC_URL", "invalid env reference"},
		{"ftp://rpc.example", "url must start with http:// or https:// (or be env:NAME)", "invalid url scheme"},
	}
	for _, tc := range cases {
		out := &setRPCOutput{}
		err := cmd.setRPC(out, "eth", tc.url)
		if err == nil || err.Error() != tc.err {
			t.Fatalf("error = %v, want %s", err, tc.err)
		}
		if len(out.errors) != 1 || out.errors[0] != tc.printed {
			t.Fatalf("printed %#v, want %s", out.errors, tc.printed)
		}
	}
	if store.finds != 0 || store.updates != 0 {
		t.Fatal("store was called for a rejected endpoint")
	}
}

func TestChainsSetRPCReportsAMissingChain(t *testing.T) {
	store := &setRPCStore{findErr: models.ErrRepositoryNotFound}
	cmd := NewChainsSetRPC(store)
	out := &setRPCOutput{}
	err := cmd.setRPC(out, " eth ", setRPCFixture)
	if err == nil || err.Error() != "chain not found: eth" {
		t.Fatalf("error = %v", err)
	}
	if len(out.errors) != 1 || out.errors[0] != "chain not found: eth" {
		t.Fatalf("printed %#v", out.errors)
	}
	if store.finds != 1 || store.updates != 0 || store.id != "eth" {
		t.Fatalf("finds=%d updates=%d id=%s", store.finds, store.updates, store.id)
	}
	if strings.Contains(out.text(), setRPCFixture) {
		t.Fatal("missing-chain output included the endpoint")
	}
}

func TestChainsSetRPCReportsAnEmptyChainRowAsMissing(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{}}
	out := &setRPCOutput{}
	err := NewChainsSetRPC(store).setRPC(out, "eth", setRPCFixture)
	if err == nil || err.Error() != "chain not found: eth" {
		t.Fatalf("error = %v", err)
	}
	if store.updates != 0 {
		t.Fatal("an empty chain row was updated")
	}
}

func TestChainsSetRPCReportsALoadFailure(t *testing.T) {
	inner := errors.New("connection refused")
	store := &setRPCStore{findErr: fmtWrap("find chain", inner)}
	out := &setRPCOutput{}
	err := NewChainsSetRPC(store).setRPC(out, "eth", setRPCFixture)
	if err == nil || err.Error() != "load chain eth: connection refused" || !errors.Is(err, inner) {
		t.Fatalf("error = %v", err)
	}
	if len(out.errors) != 1 || out.errors[0] != "failed to load chain eth: connection refused" {
		t.Fatalf("printed %#v", out.errors)
	}
	if store.updates != 0 {
		t.Fatal("a failed load wrote the endpoint")
	}
	if strings.Contains(out.text(), setRPCFixture) {
		t.Fatal("load failure included the endpoint")
	}
}

func TestChainsSetRPCWritesTheSealedEndpoint(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{ID: "eth"}}
	cmd := NewChainsSetRPC(store)
	cmd.encrypt = func(plaintext string) (string, error) {
		if plaintext != setRPCFixture {
			t.Fatal("sealer received a different value than the argument")
		}
		return "enc:v1:sealed", nil
	}
	out := &setRPCOutput{}
	if err := cmd.setRPC(out, "eth", "  "+setRPCFixture+"  "); err != nil {
		t.Fatal(err)
	}
	if store.updates != 1 || store.id != "eth" || store.sealed != "enc:v1:sealed" {
		t.Fatalf("update id=%s sealed-set=%t updates=%d", store.id, store.sealed != "", store.updates)
	}
	if store.sealed == setRPCFixture {
		t.Fatal("plaintext endpoint was stored")
	}
	want := "chain eth rpc_url updated (restart running processes to pick it up)"
	if len(out.infos) != 1 || out.infos[0] != want || len(out.errors) != 0 {
		t.Fatalf("printed errors=%#v infos=%#v", out.errors, out.infos)
	}
	if strings.Contains(out.text(), setRPCFixture) || strings.Contains(out.text(), "enc:v1:") {
		t.Fatal("success output included the endpoint")
	}
}

func TestChainsSetRPCSealsAnEnvironmentReference(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{ID: "sol"}}
	cmd := NewChainsSetRPC(store)
	cmd.encrypt = func(plaintext string) (string, error) {
		if plaintext != "env:SOLANA_RPC_URL" {
			t.Fatal("environment reference was rewritten before sealing")
		}
		return "enc:v1:sealed", nil
	}
	out := &setRPCOutput{}
	if err := cmd.setRPC(out, "sol", "env:SOLANA_RPC_URL"); err != nil {
		t.Fatal(err)
	}
	if store.sealed != "enc:v1:sealed" {
		t.Fatal("sealed reference was not stored")
	}
	if strings.Contains(out.text(), "SOLANA_RPC_URL") || strings.Contains(out.text(), "enc:v1:") {
		t.Fatal("output included the reference or the sealed value")
	}
}

func TestChainsSetRPCReportsAnEncryptionFailure(t *testing.T) {
	store := &setRPCStore{chain: &models.Chain{ID: "eth"}}
	cmd := NewChainsSetRPC(store)
	cmd.encrypt = func(string) (string, error) {
		return "", errors.New("cipher unavailable")
	}
	out := &setRPCOutput{}
	err := cmd.setRPC(out, "eth", setRPCFixture)
	if err == nil || !strings.Contains(err.Error(), "encrypt url") {
		t.Fatalf("error = %v", err)
	}
	if len(out.errors) != 1 || out.errors[0] != "failed to encrypt url: cipher unavailable" {
		t.Fatalf("printed %#v", out.errors)
	}
	if store.updates != 0 {
		t.Fatal("encryption failure wrote a row")
	}
	if strings.Contains(out.text(), setRPCFixture) {
		t.Fatal("encryption failure included the endpoint")
	}
}

func TestChainsSetRPCReportsNoRowsUpdated(t *testing.T) {
	store := &setRPCStore{
		chain:     &models.Chain{ID: "eth"},
		updateErr: models.ErrRepositoryNotFound,
	}
	cmd := NewChainsSetRPC(store)
	cmd.encrypt = func(string) (string, error) { return "enc:v1:sealed", nil }
	out := &setRPCOutput{}
	err := cmd.setRPC(out, "eth", setRPCFixture)
	if err == nil || err.Error() != "no rows updated for chain eth" {
		t.Fatalf("error = %v", err)
	}
	if len(out.errors) != 1 || out.errors[0] != "no rows updated for chain eth" {
		t.Fatalf("printed %#v", out.errors)
	}
	if strings.Contains(out.text(), setRPCFixture) || strings.Contains(out.text(), "enc:v1:") {
		t.Fatal("no-rows output included the endpoint")
	}
}

func TestChainsSetRPCReportsAnUpdateFailure(t *testing.T) {
	inner := errors.New("disk full")
	store := &setRPCStore{
		chain:     &models.Chain{ID: "eth"},
		updateErr: fmtWrap("update chain rpc", inner),
	}
	cmd := NewChainsSetRPC(store)
	cmd.encrypt = func(string) (string, error) { return "enc:v1:sealed", nil }
	out := &setRPCOutput{}
	err := cmd.setRPC(out, "eth", setRPCFixture)
	if err == nil || err.Error() != "update chain eth: disk full" || !errors.Is(err, inner) {
		t.Fatalf("error = %v", err)
	}
	if len(out.errors) != 1 || out.errors[0] != "failed to update chain eth: disk full" {
		t.Fatalf("printed %#v", out.errors)
	}
	if strings.Contains(out.text(), setRPCFixture) || strings.Contains(out.text(), "enc:v1:") {
		t.Fatal("update failure included the endpoint")
	}
}

func TestChainsSetRPCRequiresTheRepository(t *testing.T) {
	out := &setRPCOutput{}
	err := NewChainsSetRPC(nil).setRPC(out, "eth", setRPCFixture)
	if err == nil || err.Error() != "chain repository is not configured" {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(out.text(), setRPCFixture) {
		t.Fatal("missing-repository output included the endpoint")
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
