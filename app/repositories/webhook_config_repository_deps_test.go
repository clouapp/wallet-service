package repositories

import (
	"testing"

	"github.com/goravel/framework/contracts/database/orm"
)

type webhookConfigCipherStub struct{}

func (webhookConfigCipherStub) EncryptString(string) (string, error) { return "", nil }

func (webhookConfigCipherStub) DecryptString(string) (string, error) { return "", nil }

type webhookConfigQueryStub struct {
	orm.Query
}

func TestNewWebhookConfigRepositoryKeepsDependencies(t *testing.T) {
	cipher := webhookConfigCipherStub{}
	query := &webhookConfigQueryStub{}
	got := NewWebhookConfigRepository(WebhookConfigRepositoryDeps{
		Query:  query,
		Cipher: cipher,
	})
	if got == nil || got.cipher != cipher {
		t.Fatal("the repository dropped the cipher")
	}
	if got.Bound() != query {
		t.Fatal("the repository dropped the query")
	}

	fresh := NewWebhookConfigRepository(WebhookConfigRepositoryDeps{Cipher: cipher})
	if fresh == nil || fresh.Bound() != nil || fresh.cipher != cipher {
		t.Fatal("a nil query was filled in")
	}

	func() {
		defer func() {
			if recover() != "webhook config repository: cipher is required" {
				t.Fatal("a nil cipher was accepted")
			}
		}()
		NewWebhookConfigRepository(WebhookConfigRepositoryDeps{Query: query})
	}()
}
