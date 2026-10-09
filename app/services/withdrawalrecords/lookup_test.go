package withdrawalrecords

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type lookupStore struct {
	cancelStore
	row     *models.Withdrawal
	findErr error
}

func (s *lookupStore) FindByID(context.Context, uuid.UUID) (*models.Withdrawal, error) {
	return s.row, s.findErr
}

func (s *lookupStore) FindByIDAndWallet(_ context.Context, id, walletID uuid.UUID) (*models.Withdrawal, error) {
	if s.row != nil && (s.row.ID != id || s.row.WalletID != walletID) {
		return nil, nil
	}
	return s.row, s.findErr
}

type lookupWallets struct {
	wallet *models.Wallet
	err    error
}

func (w lookupWallets) FindByID(context.Context, uuid.UUID) (*models.Wallet, error) {
	return w.wallet, w.err
}

func TestFindInWallet_Hides_ARowOfAnotherWalletAndALookupFailure(t *testing.T) {
	walletID := uuid.New()
	row := &models.Withdrawal{ID: uuid.New(), WalletID: walletID}
	records := NewRecords(Deps{Store: &lookupStore{row: row}})

	got, err := records.FindInWallet(context.Background(), walletID, row.ID)
	if err != nil || got != row {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := records.FindInWallet(context.Background(), uuid.New(), row.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other wallet err = %v", err)
	}
	failing := NewRecords(Deps{Store: &lookupStore{findErr: errors.New("db down")}})
	if _, err := failing.FindInWallet(context.Background(), walletID, row.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lookup failure err = %v", err)
	}
}

func TestFindInAccount_Hides_AWithdrawalOutsideTheAccount(t *testing.T) {
	accountID := uuid.New()
	row := &models.Withdrawal{ID: uuid.New(), WalletID: uuid.New()}
	own := &models.Wallet{ID: row.WalletID, AccountID: &accountID}
	other := uuid.New()
	foreign := &models.Wallet{ID: row.WalletID, AccountID: &other}
	orphan := &models.Wallet{ID: row.WalletID}

	cases := map[string]struct {
		store   *lookupStore
		wallets lookupWallets
		ok      bool
	}{
		"own wallet":       {&lookupStore{row: row}, lookupWallets{wallet: own}, true},
		"foreign wallet":   {&lookupStore{row: row}, lookupWallets{wallet: foreign}, false},
		"wallet no owner":  {&lookupStore{row: row}, lookupWallets{wallet: orphan}, false},
		"missing wallet":   {&lookupStore{row: row}, lookupWallets{}, false},
		"wallet failure":   {&lookupStore{row: row}, lookupWallets{err: errors.New("db down")}, false},
		"missing withdraw": {&lookupStore{}, lookupWallets{wallet: own}, false},
		"withdraw failure": {&lookupStore{findErr: errors.New("db down")}, lookupWallets{wallet: own}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			records := NewRecords(Deps{Store: tc.store, Wallets: tc.wallets})
			got, err := records.FindInAccount(context.Background(), accountID, row.ID)
			if tc.ok {
				if err != nil || got != row {
					t.Fatalf("got %v, %v", got, err)
				}
				return
			}
			if !errors.Is(err, ErrNotFound) || got != nil {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
}

func TestCancelPending_Cancels_OnlyAPendingWithdrawalOfTheWallet(t *testing.T) {
	accountID := uuid.New()
	actorID := uuid.New()
	wallet := &models.Wallet{ID: uuid.New(), AccountID: &accountID}
	row := &models.Withdrawal{ID: uuid.New(), WalletID: wallet.ID, Status: "pending"}
	store := &lookupStore{row: row}
	activity := &cancelActivity{}
	records := NewRecords(Deps{Store: store, Activity: activity})

	got, err := records.CancelPending(context.Background(), wallet, actorID, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "cancelled" || store.status != "cancelled" || activity.row.ActorUserID != actorID {
		t.Fatalf("status %q stored %q activity %+v", got.Status, store.status, activity.row)
	}
}

func TestCancelPending_Refuses_AMissingOrSettledWithdrawal(t *testing.T) {
	accountID := uuid.New()
	wallet := &models.Wallet{ID: uuid.New(), AccountID: &accountID}
	settled := &models.Withdrawal{ID: uuid.New(), WalletID: wallet.ID, Status: models.WithdrawalStatusBroadcast}
	store := &lookupStore{row: settled}
	records := NewRecords(Deps{Store: store, Activity: &cancelActivity{}})

	if _, err := records.CancelPending(context.Background(), wallet, uuid.New(), settled.ID); !errors.Is(err, ErrNotPending) {
		t.Fatalf("settled err = %v", err)
	}
	if store.status != "" {
		t.Fatalf("a settled withdrawal was written: %q", store.status)
	}
	if _, err := records.CancelPending(context.Background(), wallet, uuid.New(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestCancelPending_Needs_AnActorAndAnAccountedWallet(t *testing.T) {
	accountID := uuid.New()
	row := &models.Withdrawal{ID: uuid.New(), Status: "pending"}
	for name, tc := range map[string]struct {
		wallet *models.Wallet
		actor  uuid.UUID
	}{
		"no actor":         {&models.Wallet{ID: uuid.New(), AccountID: &accountID}, uuid.Nil},
		"wallet no tenant": {&models.Wallet{ID: uuid.New()}, uuid.New()},
	} {
		t.Run(name, func(t *testing.T) {
			row.WalletID = tc.wallet.ID
			store := &lookupStore{row: row}
			records := NewRecords(Deps{Store: store, Activity: &cancelActivity{}})
			_, err := records.CancelPending(context.Background(), tc.wallet, tc.actor, row.ID)
			if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotPending) || store.status != "" {
				t.Fatalf("err = %v status %q", err, store.status)
			}
		})
	}
}

type lookupTransactions struct {
	tx  *models.Transaction
	err error
}

func (l lookupTransactions) FindByID(context.Context, uuid.UUID) (*models.Transaction, error) {
	return l.tx, l.err
}

func TestLookupInWallet_Joins_TheTransactionTheWithdrawalBroadcast(t *testing.T) {
	walletID := uuid.New()
	txID := uuid.New()
	withTx := &models.Withdrawal{ID: uuid.New(), WalletID: walletID, TransactionID: &txID}
	failed := &models.Withdrawal{ID: uuid.New(), WalletID: walletID}
	tx := &models.Transaction{ID: txID, TxHash: "0xhash"}

	records := NewRecords(Deps{Store: &lookupStore{row: withTx}, Transactions: lookupTransactions{tx: tx}})
	outcome, err := records.LookupInWallet(context.Background(), walletID, withTx.ID)
	if err != nil || outcome.Withdrawal != withTx || outcome.Transaction != tx {
		t.Fatalf("outcome = %+v, %v", outcome, err)
	}

	records = NewRecords(Deps{Store: &lookupStore{row: failed}})
	outcome, err = records.LookupInWallet(context.Background(), walletID, failed.ID)
	if err != nil || outcome.Withdrawal != failed || outcome.Transaction != nil {
		t.Fatalf("a withdrawal without a transaction: %+v, %v", outcome, err)
	}

	records = NewRecords(Deps{Store: &lookupStore{row: withTx}, Transactions: lookupTransactions{}})
	outcome, err = records.LookupInWallet(context.Background(), walletID, withTx.ID)
	if err != nil || outcome.Transaction != nil {
		t.Fatalf("a missing transaction row: %+v, %v", outcome, err)
	}
}

func TestLookupInWallet_Reports_ATransactionReadFailureNotAsMissing(t *testing.T) {
	walletID := uuid.New()
	txID := uuid.New()
	row := &models.Withdrawal{ID: uuid.New(), WalletID: walletID, TransactionID: &txID}
	down := errors.New("db down")

	records := NewRecords(Deps{Store: &lookupStore{row: row}, Transactions: lookupTransactions{err: down}})
	if _, err := records.LookupInWallet(context.Background(), walletID, row.ID); !errors.Is(err, down) || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}

	records = NewRecords(Deps{Store: &lookupStore{row: row}})
	if _, err := records.LookupInWallet(context.Background(), walletID, row.ID); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("no transaction reader: err = %v", err)
	}

	records = NewRecords(Deps{Store: &lookupStore{}})
	if _, err := records.LookupInWallet(context.Background(), walletID, row.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing withdrawal err = %v", err)
	}
}
