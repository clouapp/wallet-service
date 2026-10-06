package activity

import (
	"context"
	"errors"
	"testing"
)

type fakeDeleter struct {
	rows  int64
	err   error
	calls int
}

func (f *fakeDeleter) DeleteOlderThan(context.Context, int, int) (int64, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	return f.rows, nil
}

func TestRetentionDays(t *testing.T) {
	days, err := retentionDays("")
	if err != nil || days != defaultRetentionDays {
		t.Fatalf("default = %d, %v", days, err)
	}
	days, err = retentionDays("30")
	if err != nil || days != 30 {
		t.Fatalf("flag = %d, %v", days, err)
	}
	if _, err := retentionDays("0"); err == nil {
		t.Fatal("zero days was accepted")
	}
	if _, err := retentionDays("-4"); err == nil {
		t.Fatal("negative days was accepted")
	}
	if _, err := retentionDays("soon"); err == nil {
		t.Fatal("text was accepted")
	}
}

func TestPruneRejectsABadWindowBeforeDeleting(t *testing.T) {
	logRows := &fakeDeleter{}
	accountRows := &fakeDeleter{}
	service := NewService(Deps{Rows: &getReader{}, ActivityLog: logRows, AccountActivity: accountRows})
	if _, err := service.Prune(context.Background(), "soon"); err == nil {
		t.Fatal("bad window was accepted")
	}
	if logRows.calls != 0 || accountRows.calls != 0 {
		t.Fatal("a bad window deleted rows")
	}
}

func TestPruneReportsBothTables(t *testing.T) {
	service := NewService(Deps{
		Rows:            &getReader{},
		ActivityLog:     &fakeDeleter{rows: 2},
		AccountActivity: &fakeDeleter{rows: 3},
	})
	line, err := service.Prune(context.Background(), "10")
	if err != nil {
		t.Fatal(err)
	}
	want := "pruned 2 activity_log rows and 3 account_activity rows older than 10 days"
	if line != want {
		t.Fatalf("line = %q", line)
	}
}

func TestPruneStopsWhenADeleteFails(t *testing.T) {
	accountRows := &fakeDeleter{}
	service := NewService(Deps{
		Rows:            &getReader{},
		ActivityLog:     &fakeDeleter{err: errors.New("db down")},
		AccountActivity: accountRows,
	})
	if _, err := service.Prune(context.Background(), ""); err == nil {
		t.Fatal("delete failure was ignored")
	}
	if accountRows.calls != 0 {
		t.Fatal("account rows were deleted after the log delete failed")
	}
}
