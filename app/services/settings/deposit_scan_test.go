package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestEffectiveDepositScan_MissingRowKeepsZeros(t *testing.T) {
	t.Parallel()

	service := newTestService(newMemoryStore())
	got, err := service.EffectiveDepositScan(context.Background())
	if err != nil {
		t.Fatalf("effective scan: %v", err)
	}
	if got != (DepositScanValues{}) {
		t.Fatalf("effective = %+v, want zeros", got)
	}
}

func TestEffectiveDepositScan_StoredRowOverridesOneKey(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupDepositScan, map[string]string{keyBatchBlocks: "80"})
	accountID := uuid.New()
	if err := store.UpsertMany(context.Background(), accountID, groupDepositScan, map[string]string{
		keyCatchUpBlocks: "999",
	}); err != nil {
		t.Fatalf("store account row: %v", err)
	}

	got, err := newTestService(store).EffectiveDepositScan(context.Background())
	if err != nil {
		t.Fatalf("effective scan: %v", err)
	}
	if got != (DepositScanValues{BatchBlocks: 80}) {
		t.Fatalf("effective = %+v, want batch 80 and the other fields unset", got)
	}
}

func TestEffectiveDepositScan_InvalidKeyFallsBackToZero(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupDepositScan, map[string]string{
		keyBatchBlocks:     "40",
		keyCatchUpBlocks:   "nope",
		keyScanConcurrency: "0",
	})

	got, err := newTestService(store).EffectiveDepositScan(context.Background())
	if err != nil {
		t.Fatalf("effective scan: %v", err)
	}
	if got != (DepositScanValues{BatchBlocks: 40}) {
		t.Fatalf("effective = %+v, want only the valid batch", got)
	}
}

func TestEffectiveDepositScan_ConcurrencyAboveTheCeilingFallsBack(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupDepositScan, map[string]string{
		keyScanConcurrency: "33",
		keyCatchUpBlocks:   "200",
	})

	got, err := newTestService(store).EffectiveDepositScan(context.Background())
	if err != nil {
		t.Fatalf("effective scan: %v", err)
	}
	if got != (DepositScanValues{CatchUpBlocks: 200}) {
		t.Fatalf("effective = %+v, want catch-up 200 and concurrency unset", got)
	}
}

func TestEffectiveDepositScan_StoreError(t *testing.T) {
	t.Parallel()

	service := newTestService(platformErrStore{err: errors.New("db down")})
	_, err := service.EffectiveDepositScan(context.Background())
	if err == nil || err.Error() != "db down" {
		t.Fatalf("error = %v, want db down", err)
	}
}

func TestEffectiveDepositScan_RequiresAPlatformReader(t *testing.T) {
	t.Parallel()

	service := newTestService(errStore{err: errors.New("db down")})
	_, err := service.EffectiveDepositScan(context.Background())
	if err == nil {
		t.Fatal("expected an error when the store cannot read platform rows")
	}
}

func TestEffectiveDepositScan_RejectsANilContext(t *testing.T) {
	t.Parallel()

	_, err := newTestService(newMemoryStore()).EffectiveDepositScan(nil)
	if err == nil {
		t.Fatal("expected an error for a nil context")
	}
}

func TestAccountRegistryOmitsDepositScan(t *testing.T) {
	t.Parallel()

	view, err := newTestService(newMemoryStore()).Registry(context.Background(), uuid.New(), "owner")
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	for _, section := range view.Sections {
		for _, block := range section.Blocks {
			for _, group := range block.Groups {
				if group.Name == groupDepositScan || group.Name == groupWebhookDelivery || group.Name == groupMailSMTP {
					t.Fatalf("%s is a platform group and must stay off the account document", group.Name)
				}
			}
		}
	}
}

func TestSaveDepositScanIsNotAnAccountGroup(t *testing.T) {
	t.Parallel()

	_, err := newTestService(newMemoryStore()).Save(
		context.Background(), uuid.New(), uuid.New(), "owner", groupDepositScan,
		map[string]any{keyBatchBlocks: 10},
	)
	if !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("error = %v, want group not found", err)
	}
}

func TestValidateDepositScan(t *testing.T) {
	t.Parallel()

	group, ok := FindGroup(groupDepositScan)
	if !ok || group.Validate == nil {
		t.Fatal("deposit_scan validator is missing")
	}
	if err := group.Validate(map[string]string{
		keyBatchBlocks: "10", keyCatchUpBlocks: "20", keyScanConcurrency: "4",
	}); err != nil {
		t.Fatalf("valid window: %v", err)
	}
	if err := group.Validate(map[string]string{
		keyBatchBlocks: "0", keyCatchUpBlocks: "0", keyScanConcurrency: "0",
	}); err != nil {
		t.Fatalf("zeros are the environment default: %v", err)
	}
	err := group.Validate(map[string]string{
		keyBatchBlocks: "30", keyCatchUpBlocks: "10", keyScanConcurrency: "99",
	})
	validation, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error = %v, want a validation error", err)
	}
	if len(validation.Fields[keyCatchUpBlocks]) == 0 || len(validation.Fields[keyScanConcurrency]) == 0 {
		t.Fatalf("fields = %#v", validation.Fields)
	}
}

type platformErrStore struct {
	err error
}

func (s platformErrStore) ListGroup(context.Context, uuid.UUID, string) ([]models.Setting, error) {
	return nil, s.err
}

func (s platformErrStore) UpsertMany(context.Context, uuid.UUID, string, map[string]string) error {
	return s.err
}

func (s platformErrStore) ListPlatform(context.Context, string) ([]models.Setting, error) {
	return nil, s.err
}
