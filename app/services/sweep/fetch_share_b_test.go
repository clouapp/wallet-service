package sweep

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestFetchShareB_NilReaderReportsSecretsManagerNotConfigured(t *testing.T) {
	svc := &service{}
	_, err := svc.fetchShareB(context.Background(), &models.Wallet{MPCSecretARN: "arn:secret"})
	if err == nil || err.Error() != "sweep: secrets manager not configured" {
		t.Fatal("nil reader must keep the not-configured error")
	}
}

func TestFetchShareB_EmptyARNIsRejectedBeforeTheReader(t *testing.T) {
	reader := &recordingSecret{err: errors.New("must not be called")}
	svc := &service{secrets: reader}
	_, err := svc.fetchShareB(context.Background(), &models.Wallet{})
	if err == nil || err.Error() != "sweep: wallet has no MPC secret ARN" {
		t.Fatal("empty ARN must keep its error")
	}
	if reader.called {
		t.Fatal("empty ARN must not call the reader")
	}
}

func TestFetchShareB_WrapsTheReaderErrorAndKeepsTheARN(t *testing.T) {
	const arn = "arn:aws:secretsmanager:us-east-1:1:secret:vault/wallet/x/share-b"
	reader := &recordingSecret{err: errors.New("down")}
	svc := &service{secrets: reader}
	_, err := svc.fetchShareB(context.Background(), &models.Wallet{MPCSecretARN: arn})
	if err == nil || err.Error() != "sweep: fetch share_b: down" {
		t.Fatal("reader error must stay wrapped")
	}
	if reader.id != arn {
		t.Fatal("secret id changed")
	}
}

func TestFetchShareB_ReturnsTheReaderBytes(t *testing.T) {
	want := []byte{0x0a, 0x0b, 0x0c}
	reader := &recordingSecret{bin: want}
	svc := &service{secrets: reader}
	got, err := svc.fetchShareB(context.Background(), &models.Wallet{MPCSecretARN: "arn:secret"})
	if err != nil {
		t.Fatal("read failed")
	}
	if !bytes.Equal(got, want) {
		t.Fatal("secret bytes changed")
	}
}

type recordingSecret struct {
	id     string
	bin    []byte
	err    error
	called bool
}

func (r *recordingSecret) Binary(_ context.Context, secretID string) ([]byte, error) {
	r.called = true
	r.id = secretID
	if r.err != nil {
		return nil, r.err
	}
	return r.bin, nil
}
