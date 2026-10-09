package repositories_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/pkg/pgerr"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func TestNew_Webhook_ConfigRepositoryKeepsDependencies(t *testing.T) {
	fixtures.TestDB(t)

	ctx := context.Background()
	cipher := facades.Crypt()

	func() {
		defer func() {
			if recover() != "webhook config repository: cipher is required" {
				t.Fatal("a nil cipher was accepted")
			}
		}()
		repositories.NewWebhookConfigRepository(facades.Orm().Query(), nil)
	}()

	fresh := repositories.NewWebhookConfigRepository(nil, cipher)
	if fresh == nil {
		t.Fatal("a nil query was refused")
	}

	id := uuid.New()
	cfg := &models.WebhookConfig{
		ID:       id,
		URL:      "https://repo-real.test/" + id.String(),
		Secret:   "whsec_plain",
		Events:   `{"deposit.confirmed"}`,
		IsActive: true,
	}
	if err := fresh.Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	found, err := fresh.FindByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if found.URL != cfg.URL || found.Secret != "whsec_plain" {
		t.Fatalf("query returned url %q", found.URL)
	}

	err = fresh.Create(ctx, &models.WebhookConfig{
		ID:       id,
		URL:      "https://repo-real.test/dup/" + id.String(),
		Secret:   "other",
		Events:   `{"deposit.confirmed"}`,
		IsActive: true,
	})
	if !pgerr.IsUniqueViolation(err) {
		t.Fatal("the primary key constraint was not mapped as a unique violation")
	}

	_, err = fresh.FindOwnership(ctx, uuid.New())
	if !errors.Is(err, models.ErrRepositoryNotFound) {
		t.Fatal("a missing row was not mapped to ErrRepositoryNotFound")
	}
}
