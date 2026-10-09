package account

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type viewAccounts struct {
	AccountStore
	created  []models.Account
	statuses []string
	names    []string
	err      error
}

func (a *viewAccounts) Create(_ context.Context, account *models.Account) error {
	if a.err != nil {
		return a.err
	}
	a.created = append(a.created, *account)
	return nil
}

func (a *viewAccounts) SetStatus(_ context.Context, _ uuid.UUID, status string) error {
	if a.err != nil {
		return a.err
	}
	a.statuses = append(a.statuses, status)
	return nil
}

func (a *viewAccounts) SetName(_ context.Context, _ uuid.UUID, name string) error {
	a.names = append(a.names, name)
	return nil
}

func (a *viewAccounts) SetViewAllWallets(context.Context, uuid.UUID, bool) error {
	return nil
}

type viewMemberships struct {
	MembershipStore
	created []models.AccountUser
}

func (m *viewMemberships) Create(_ context.Context, member *models.AccountUser) error {
	m.created = append(m.created, *member)
	return nil
}

type fixedSweepLimits struct {
	document *string
	err      error
}

func (l fixedSweepLimits) AccountSweepLimitsWire(context.Context, uuid.UUID) (*string, error) {
	return l.document, l.err
}

type fixedFeatures struct {
	names []string
	err   error
}

func (f fixedFeatures) ActiveForAccount(context.Context, uuid.UUID) ([]string, error) {
	return f.names, f.err
}

func newViewService(accounts *viewAccounts, limits fixedSweepLimits, features fixedFeatures) *Service {
	return NewService(Deps{
		Accounts:    accounts,
		Memberships: &viewMemberships{},
		SweepLimits: limits,
		Features:    features,
	})
}

func TestCreate_For_OwnerReturnsTheAccountWithItsSweepLimits(t *testing.T) {
	limits := `{"daily":"5"}`
	accounts := &viewAccounts{}
	ownerID := uuid.New()

	view, err := newViewService(accounts, fixedSweepLimits{document: &limits}, fixedFeatures{}).
		CreateForOwner(context.Background(), CreateAccountInput{Name: "Acme", OwnerID: ownerID})
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts.created) != 1 || view.Account.ID != accounts.created[0].ID || view.Account.Name != "Acme" || view.Account.Status != models.StatusActive {
		t.Fatalf("view = %+v, created = %+v", view, accounts.created)
	}
	if view.SweepLimits == nil || *view.SweepLimits != limits {
		t.Fatalf("sweep limits = %v", view.SweepLimits)
	}
}

func TestCreate_For_OwnerTellsAFailedInsertFromAFailedRead(t *testing.T) {
	outage := errors.New("store unavailable")

	_, err := newViewService(&viewAccounts{err: outage}, fixedSweepLimits{}, fixedFeatures{}).
		CreateForOwner(context.Background(), CreateAccountInput{Name: "Acme", OwnerID: uuid.New()})
	if !errors.Is(err, ErrAccountNotCreated) || !errors.Is(err, outage) {
		t.Fatalf("insert failure = %v, want ErrAccountNotCreated wrapping the cause", err)
	}

	_, err = newViewService(&viewAccounts{}, fixedSweepLimits{err: outage}, fixedFeatures{}).
		CreateForOwner(context.Background(), CreateAccountInput{Name: "Acme", OwnerID: uuid.New()})
	if !errors.Is(err, outage) || errors.Is(err, ErrAccountNotCreated) {
		t.Fatalf("limits failure = %v, want the read error and not ErrAccountNotCreated", err)
	}
}

func TestDetail_Lists_TheActiveFeatures(t *testing.T) {
	account := &models.Account{ID: uuid.New(), Name: "Acme"}

	detail, err := newViewService(&viewAccounts{}, fixedSweepLimits{}, fixedFeatures{names: []string{"sweep-enabled"}}).
		Detail(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Account.ID != account.ID || len(detail.Features) != 1 || detail.Features[0] != "sweep-enabled" {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestDetail_Logs_AFeatureReadThatFailed(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	// The request context holds the session JWT; that value is not written.
	const fixture = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.fixture-signature"
	ctx := context.WithValue(context.Background(), struct{ name string }{"session"}, fixture)

	_, err := newViewService(&viewAccounts{}, fixedSweepLimits{}, fixedFeatures{err: errors.New("load features")}).
		Detail(ctx, &models.Account{ID: uuid.New()})
	if err == nil {
		t.Fatal("a failed feature read was served")
	}
	if !strings.Contains(logs.String(), "account: active features") {
		t.Fatal("expected an error log that active features could not be read")
	}
	if strings.Contains(logs.String(), fixture) {
		t.Fatal("active features log wrote the session JWT")
	}
}

func TestArchive_And_FreezeStoreTheStatusAndReturnTheView(t *testing.T) {
	for status, change := range map[string]func(*Service, *models.Account) (View, error){
		models.AccountStatusArchived: func(s *Service, a *models.Account) (View, error) { return s.Archive(context.Background(), a) },
		models.AccountStatusFrozen:   func(s *Service, a *models.Account) (View, error) { return s.Freeze(context.Background(), a) },
	} {
		t.Run(status, func(t *testing.T) {
			accounts := &viewAccounts{}
			account := &models.Account{ID: uuid.New(), Status: models.StatusActive}

			view, err := change(newViewService(accounts, fixedSweepLimits{}, fixedFeatures{}), account)
			if err != nil {
				t.Fatal(err)
			}
			if len(accounts.statuses) != 1 || accounts.statuses[0] != status || view.Account.Status != status {
				t.Fatalf("stored %v, view status %q", accounts.statuses, view.Account.Status)
			}

			accounts.err = errors.New("store unavailable")
			if _, err := change(newViewService(accounts, fixedSweepLimits{}, fixedFeatures{}), account); err == nil {
				t.Fatal("a failed status write was served")
			}
		})
	}
}

func TestUpdate_Account_LeavesABlankNameAsStored(t *testing.T) {
	accounts := &viewAccounts{}
	account := &models.Account{ID: uuid.New(), Name: "Acme"}
	viewAll := true

	view, err := newViewService(accounts, fixedSweepLimits{}, fixedFeatures{}).
		UpdateAccount(context.Background(), account, UpdateAccountInput{ViewAllWallets: &viewAll})
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts.names) != 0 || view.Account.Name != "Acme" || !view.Account.ViewAllWallets {
		t.Fatalf("names written %v, view = %+v", accounts.names, view.Account)
	}
}
