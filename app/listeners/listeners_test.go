package listeners

import "testing"

func TestEnqueueWalletRefreshSignature(t *testing.T) {
	l := &EnqueueWalletRefresh{}
	if l.Signature() != "enqueue_wallet_refresh" {
		t.Fatalf("unexpected signature: %s", l.Signature())
	}
}

func TestEnqueueTransactionRefreshSignature(t *testing.T) {
	l := &EnqueueTransactionRefresh{}
	if l.Signature() != "enqueue_transaction_refresh" {
		t.Fatalf("unexpected signature: %s", l.Signature())
	}
}

func TestEnqueueUTXORefreshSignature(t *testing.T) {
	l := &EnqueueUTXORefresh{}
	if l.Signature() != "enqueue_utxo_refresh" {
		t.Fatalf("unexpected signature: %s", l.Signature())
	}
}

func TestEnqueueWalletRefreshQueueConfig(t *testing.T) {
	l := &EnqueueWalletRefresh{}
	q := l.Queue()
	if !q.Enable {
		t.Fatal("queue should be enabled")
	}
	if q.Connection != "database" {
		t.Fatalf("expected database connection, got %s", q.Connection)
	}
	if q.Queue != "blockchain" {
		t.Fatalf("expected blockchain queue, got %s", q.Queue)
	}
}

func TestEnqueueTransactionRefreshQueueConfig(t *testing.T) {
	l := &EnqueueTransactionRefresh{}
	q := l.Queue()
	if !q.Enable {
		t.Fatal("queue should be enabled")
	}
	if q.Connection != "database" {
		t.Fatalf("expected database connection, got %s", q.Connection)
	}
	if q.Queue != "blockchain" {
		t.Fatalf("expected blockchain queue, got %s", q.Queue)
	}
}

func TestEnqueueUTXORefreshQueueConfig(t *testing.T) {
	l := &EnqueueUTXORefresh{}
	q := l.Queue()
	if !q.Enable {
		t.Fatal("queue should be enabled")
	}
	if q.Connection != "database" {
		t.Fatalf("expected database connection, got %s", q.Connection)
	}
	if q.Queue != "blockchain" {
		t.Fatalf("expected blockchain queue, got %s", q.Queue)
	}
}

func TestEnqueueWalletRefreshRejectsEmptyArgs(t *testing.T) {
	l := &EnqueueWalletRefresh{}
	err := l.Handle()
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestEnqueueWalletRefreshRejectsEmptyWalletID(t *testing.T) {
	l := &EnqueueWalletRefresh{}
	err := l.Handle("", "eth")
	if err == nil {
		t.Fatal("expected error for empty wallet_id")
	}
}

func TestEnqueueWalletRefreshRejectsEmptyChainID(t *testing.T) {
	l := &EnqueueWalletRefresh{}
	err := l.Handle("wallet-1", "")
	if err == nil {
		t.Fatal("expected error for empty chain_id")
	}
}

func TestEnqueueTransactionRefreshRejectsEmptyArgs(t *testing.T) {
	l := &EnqueueTransactionRefresh{}
	err := l.Handle()
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestEnqueueTransactionRefreshRejectsEmptyWalletID(t *testing.T) {
	l := &EnqueueTransactionRefresh{}
	err := l.Handle("", "eth")
	if err == nil {
		t.Fatal("expected error for empty wallet_id")
	}
}

func TestEnqueueTransactionRefreshRejectsEmptyChainID(t *testing.T) {
	l := &EnqueueTransactionRefresh{}
	err := l.Handle("wallet-1", "")
	if err == nil {
		t.Fatal("expected error for empty chain_id")
	}
}

func TestEnqueueUTXORefreshRejectsEmptyArgs(t *testing.T) {
	l := &EnqueueUTXORefresh{}
	err := l.Handle()
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestEnqueueUTXORefreshRejectsEmptyWalletID(t *testing.T) {
	l := &EnqueueUTXORefresh{}
	err := l.Handle("", "btc")
	if err == nil {
		t.Fatal("expected error for empty wallet_id")
	}
}

func TestEnqueueUTXORefreshRejectsEmptyChainID(t *testing.T) {
	l := &EnqueueUTXORefresh{}
	err := l.Handle("wallet-1", "")
	if err == nil {
		t.Fatal("expected error for empty chain_id")
	}
}

func TestEnqueueWalletRefreshRejectsSingleArg(t *testing.T) {
	l := &EnqueueWalletRefresh{}
	err := l.Handle("wallet-1")
	if err == nil {
		t.Fatal("expected error for single arg")
	}
}

func TestEnqueueTransactionRefreshRejectsSingleArg(t *testing.T) {
	l := &EnqueueTransactionRefresh{}
	err := l.Handle("wallet-1")
	if err == nil {
		t.Fatal("expected error for single arg")
	}
}

func TestEnqueueUTXORefreshRejectsSingleArg(t *testing.T) {
	l := &EnqueueUTXORefresh{}
	err := l.Handle("wallet-1")
	if err == nil {
		t.Fatal("expected error for single arg")
	}
}

func TestEnqueueWalletRefreshRejectsNonStringWalletID(t *testing.T) {
	l := &EnqueueWalletRefresh{}
	err := l.Handle(123, "eth")
	if err == nil {
		t.Fatal("expected error for non-string wallet_id")
	}
}

func TestEnqueueTransactionRefreshRejectsNonStringWalletID(t *testing.T) {
	l := &EnqueueTransactionRefresh{}
	err := l.Handle(123, "eth")
	if err == nil {
		t.Fatal("expected error for non-string wallet_id")
	}
}

func TestEnqueueUTXORefreshRejectsNonStringWalletID(t *testing.T) {
	l := &EnqueueUTXORefresh{}
	err := l.Handle(123, "btc")
	if err == nil {
		t.Fatal("expected error for non-string wallet_id")
	}
}
