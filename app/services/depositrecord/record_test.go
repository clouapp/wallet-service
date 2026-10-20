package depositrecord

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// memoryStore holds deposit rows like the transactions table does: one row per
// (chain, tx_hash, log_index), a second insert or a move onto a taken position
// fails with a unique violation. Within holds one lock, as the transaction lock
// serializes the Records of one transaction.
type memoryStore struct {
	txMu      sync.Mutex
	locks     int
	lockErr   error
	mu        sync.Mutex
	rows      []models.Transaction
	findErr   error
	createErr error
	// beforeClaim runs once, before the next claim is applied, to model another
	// process writing between this one's read and its claim.
	beforeClaim func(*memoryStore)
	creates     int
	claims      int
}

func uniqueViolation() error {
	return fmt.Errorf("create transaction: %w", errors.New(`ERROR: duplicate key value violates unique constraint "transactions_deposit_chain_tx_log_unique" (SQLSTATE 23505)`))
}

func (m *memoryStore) Within(ctx context.Context, fn func(context.Context) error) error {
	m.txMu.Lock()
	defer m.txMu.Unlock()
	return fn(ctx)
}

func (m *memoryStore) LockTransactionDeposits(context.Context, string, string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locks++
	return m.lockErr
}

func (m *memoryStore) FindDepositsByChainAndTxHash(_ context.Context, chainID, txHash string) ([]models.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.findErr != nil {
		return nil, m.findErr
	}
	var found []models.Transaction
	for _, row := range m.rows {
		if row.Chain == chainID && row.TxHash == txHash {
			found = append(found, row)
		}
	}
	return found, nil
}

func (m *memoryStore) ClaimDepositPosition(_ context.Context, id uuid.UUID, position int) (bool, error) {
	m.mu.Lock()
	hook := m.beforeClaim
	m.beforeClaim = nil
	m.mu.Unlock()
	if hook != nil {
		hook(m)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claims++
	for i, row := range m.rows {
		if row.ID != id {
			continue
		}
		if row.LogIndex != types.NativeValuePosition {
			return false, nil
		}
		if m.taken(row.Chain, row.TxHash, position) {
			return false, uniqueViolation()
		}
		m.rows[i].LogIndex = position
		return true, nil
	}
	return false, nil
}

func (m *memoryStore) Create(_ context.Context, tx *models.Transaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return m.createErr
	}
	if m.taken(tx.Chain, tx.TxHash, tx.LogIndex) {
		return uniqueViolation()
	}
	m.creates++
	m.rows = append(m.rows, *tx)
	return nil
}

func (m *memoryStore) taken(chainID, txHash string, position int) bool {
	for _, row := range m.rows {
		if row.Chain == chainID && row.TxHash == txHash && row.LogIndex == position {
			return true
		}
	}
	return false
}

func (m *memoryStore) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rows)
}

func (m *memoryStore) row(id uuid.UUID) models.Transaction {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.rows {
		if row.ID == id {
			return row
		}
	}
	return models.Transaction{}
}

const (
	testChain     = "eth"
	testTx        = "0xbatch"
	testBlockHash = "0xblock"
	testUSDT      = "0xusdt"
)

// deposit is the row a source builds for one detected transfer.
func deposit(source string, position int, to, contract, amount string) *models.Transaction {
	return &models.Transaction{
		ID:            uuid.New(),
		Chain:         testChain,
		TxType:        models.TxTypeDeposit,
		TxHash:        testTx,
		LogIndex:      position,
		ToAddress:     to,
		TokenContract: contract,
		Amount:        amount,
		BlockNumber:   100,
		BlockHash:     testBlockHash,
		Source:        source,
	}
}

func record(t *testing.T, store *memoryStore, row *models.Transaction) Outcome {
	t.Helper()
	outcome, err := Record(context.Background(), store, row, nil)
	require.NoError(t, err)
	return outcome
}

func TestRecord_Inserts_EachTransferOfOneTransactionAtItsOwnPosition(t *testing.T) {
	store := &memoryStore{}

	assert.Equal(t, Created, record(t, store, deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10")))
	assert.Equal(t, Created, record(t, store, deposit(models.TxSourceChain, 7, "0xbob", testUSDT, "20")))
	assert.Equal(t, Created, record(t, store, deposit(models.TxSourceChain, 9, "0xbob", testUSDT, "20")),
		"a second identical Transfer log to the same address is its own deposit")
	assert.Equal(t, Created, record(t, store, deposit(models.TxSourceChain, types.NativeValuePosition, "0xbob", "", "5")),
		"the native value of the transaction is a deposit beside its token transfers")
	assert.Equal(t, 4, store.count())
}

func TestRecord_Skips_ATransferWhosePositionIsRecorded(t *testing.T) {
	store := &memoryStore{}
	first := deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10")
	require.Equal(t, Created, record(t, store, first))

	again := deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10")
	assert.Equal(t, SamePosition, record(t, store, again))
	assert.Equal(t, 1, store.count())
	assert.Equal(t, 1, store.creates)
}

func TestRecord_Skips_ThePushOfATransferTheScannerRecorded(t *testing.T) {
	store := &memoryStore{}
	require.Equal(t, Created, record(t, store, deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10")))

	assert.Equal(t, SamePosition, record(t, store, deposit(models.TxSourceDepositIngest, 3, "0xalice", testUSDT, "10")))
	assert.Equal(t, 1, store.count())
}

func TestRecord_Skips_ATransactionRecordedFromAnotherBlock(t *testing.T) {
	store := &memoryStore{}
	orphaned := deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10")
	orphaned.BlockHash = "0xorphaned"
	require.Equal(t, Created, record(t, store, orphaned))

	// The transaction was mined again in the next block after a reorganization:
	// its log index there is another one.
	reincluded := deposit(models.TxSourceChain, 41, "0xalice", testUSDT, "10")
	reincluded.BlockNumber = 101
	reincluded.BlockHash = "0xcanonical"
	assert.Equal(t, OtherBlock, record(t, store, reincluded))
	assert.Equal(t, 1, store.count())
}

func TestRecord_Skips_ATransactionMinedAgainAtTheSameHeight(t *testing.T) {
	store := &memoryStore{}
	require.Equal(t, Created, record(t, store, deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10")))

	// A one-block reorganization: another block at the same height holds it.
	replaced := deposit(models.TxSourceChain, 12, "0xalice", testUSDT, "10")
	replaced.BlockHash = "0xreplacement"
	assert.Equal(t, OtherBlock, record(t, store, replaced))
	assert.Equal(t, 1, store.count())
}

func TestRecord_SameBlock_ComparesNumbersWhenAHashIsMissing(t *testing.T) {
	store := &memoryStore{}
	pushed := deposit(models.TxSourceDepositIngest, 3, "0xalice", testUSDT, "10")
	pushed.BlockHash = "" // a provider push without the block hash
	require.Equal(t, Created, record(t, store, pushed))

	twin := deposit(models.TxSourceDepositIngest, 4, "0xalice", testUSDT, "10")
	assert.Equal(t, Created, record(t, store, twin), "same block number, one hash unknown: the same block, so a twin transfer")

	elsewhere := deposit(models.TxSourceDepositIngest, 9, "0xalice", testUSDT, "10")
	elsewhere.BlockHash = ""
	elsewhere.BlockNumber = 105
	assert.Equal(t, OtherBlock, record(t, store, elsewhere))
}

func TestRecord_SameBlock_ARowWithoutABlockIsNotAnotherBlock(t *testing.T) {
	store := &memoryStore{}
	unplaced := deposit(models.TxSourceDepositIngest, 3, "0xalice", testUSDT, "10")
	unplaced.BlockHash, unplaced.BlockNumber = "", 0
	require.Equal(t, Created, record(t, store, unplaced))

	assert.Equal(t, Created, record(t, store, deposit(models.TxSourceDepositIngest, 7, "0xalice", testUSDT, "10")))
}

func TestRecord_Inserts_ADifferentTransferOfATransactionRecordedFromAnotherBlock(t *testing.T) {
	store := &memoryStore{}
	orphaned := deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10")
	orphaned.BlockHash = "0xorphaned"
	require.Equal(t, Created, record(t, store, orphaned))

	// The first scan, of the orphaned block, did not record bob (its address was not
	// watched yet); the transaction mined again pays him too.
	bob := deposit(models.TxSourceChain, 42, "0xbob", testUSDT, "20")
	bob.BlockNumber, bob.BlockHash = 101, "0xcanonical"
	assert.Equal(t, Created, record(t, store, bob))
}

func TestRecord_Skips_ADifferentTransferAtARecordedPosition(t *testing.T) {
	store := &memoryStore{}
	require.Equal(t, Created, record(t, store, deposit(models.TxSourceDepositIngest, types.NativeValuePosition, "0xalice", "", "10")))

	// A second contract-made native transfer of the same transaction.
	assert.Equal(t, PositionTaken, record(t, store, deposit(models.TxSourceDepositIngest, types.NativeValuePosition, "0xbob", "", "20")))
	assert.Equal(t, 1, store.count())
}

func TestRecord_Claims_TheRowTheScannerRecordedBeforePositions(t *testing.T) {
	store := &memoryStore{}
	legacy := deposit(models.TxSourceChain, types.NativeValuePosition, "0xalice", testUSDT, "10")
	store.rows = append(store.rows, *legacy)

	rescanned := deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10")
	assert.Equal(t, ClaimedUnpositioned, record(t, store, rescanned))
	assert.Equal(t, 1, store.count(), "the old row is the deposit: it is moved, not duplicated")
	assert.Equal(t, 3, store.row(legacy.ID).LogIndex, "the old row keeps its id and takes the position")

	// The transfer the one-per-transaction rule left out is recorded now.
	sibling := deposit(models.TxSourceChain, 7, "0xbob", testUSDT, "20")
	assert.Equal(t, Created, record(t, store, sibling))
	assert.Equal(t, 2, store.count())
}

func TestRecord_Claims_TheOldRowOnceWhenTwoTransfersLookTheSame(t *testing.T) {
	store := &memoryStore{}
	legacy := deposit(models.TxSourceChain, types.NativeValuePosition, "0xalice", testUSDT, "10")
	store.rows = append(store.rows, *legacy)

	assert.Equal(t, ClaimedUnpositioned, record(t, store, deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10")))
	assert.Equal(t, Created, record(t, store, deposit(models.TxSourceChain, 9, "0xalice", testUSDT, "10")))
	assert.Equal(t, 2, store.count())
}

func TestRecord_Claim_ComparesAddressesAndContractsWithoutCase(t *testing.T) {
	store := &memoryStore{}
	legacy := deposit(models.TxSourceChain, types.NativeValuePosition, "0xAlice", "0xUSDT", "10")
	store.rows = append(store.rows, *legacy)

	assert.Equal(t, ClaimedUnpositioned, record(t, store, deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10")))
	assert.Equal(t, 1, store.count())
}

func TestRecord_Claim_NeedsTheSameRecipientAssetAndAmount(t *testing.T) {
	cases := map[string]*models.Transaction{
		"recipient": deposit(models.TxSourceChain, 3, "0xbob", testUSDT, "10"),
		"asset":     deposit(models.TxSourceChain, 3, "0xalice", "0xusdc", "10"),
		"amount":    deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "11"),
	}
	for name, transfer := range cases {
		t.Run(name, func(t *testing.T) {
			store := &memoryStore{}
			legacy := deposit(models.TxSourceChain, types.NativeValuePosition, "0xalice", testUSDT, "10")
			store.rows = append(store.rows, *legacy)

			assert.Equal(t, Created, record(t, store, transfer))
			assert.Equal(t, types.NativeValuePosition, store.row(legacy.ID).LogIndex)
			assert.Zero(t, store.claims)
		})
	}
}

func TestRecord_Claims_AnOldRowOfANativeTransferThatHasAPosition(t *testing.T) {
	store := &memoryStore{}
	// A Bitcoin output or a lamport credit recorded before positions: the native
	// asset, at the old scanner value.
	legacy := deposit(models.TxSourceChain, types.NativeValuePosition, "bc1alice", "", "5000")
	store.rows = append(store.rows, *legacy)

	assert.Equal(t, ClaimedUnpositioned, record(t, store, deposit(models.TxSourceChain, 1, "bc1alice", "", "5000")))
	assert.Equal(t, 1, store.row(legacy.ID).LogIndex)
	assert.Equal(t, Created, record(t, store, deposit(models.TxSourceChain, 2, "bc1bob", "", "7000")))
	assert.Equal(t, 2, store.count())
}

func TestRecord_Skips_ATransferTheOtherSourceRecordedAtAnotherPosition(t *testing.T) {
	store := &memoryStore{}
	require.Equal(t, Created, record(t, store, deposit(models.TxSourceDepositIngest, 30, "owner", "mint", "2200")))

	// The scanner numbers the same credit differently (the provider listed the
	// accounts in another order): it is the same transfer, recorded once.
	assert.Equal(t, OtherSource, record(t, store, deposit(models.TxSourceChain, 28, "owner", "mint", "2200")))
	assert.Equal(t, 1, store.count())
}

func TestRecord_Skips_APushOfATransferTheScannerRecordedBeforePositions(t *testing.T) {
	store := &memoryStore{}
	legacy := deposit(models.TxSourceChain, types.NativeValuePosition, "0xalice", testUSDT, "10")
	store.rows = append(store.rows, *legacy)

	assert.Equal(t, OtherSource, record(t, store, deposit(models.TxSourceDepositIngest, 3, "0xalice", testUSDT, "10")))
	assert.Equal(t, types.NativeValuePosition, store.row(legacy.ID).LogIndex, "only the scanner moves its own old row")
	assert.Equal(t, 1, store.count())
}

func TestRecord_Inserts_ATwinTheOtherSourceDidNotRecordWhenThisSourceReportsBoth(t *testing.T) {
	store := &memoryStore{}
	// The push recorded output 0 and failed before output 2, which pays the same
	// address the same amount.
	require.Equal(t, Created, record(t, store, deposit(models.TxSourceDepositIngest, 0, "bc1alice", "", "5000")))
	siblings := []Transfer{
		{Position: 0, To: "bc1alice", Amount: "5000"},
		{Position: 2, To: "bc1alice", Amount: "5000"},
	}

	outcome, err := Record(context.Background(), store, deposit(models.TxSourceChain, 2, "bc1alice", "", "5000"), siblings)
	require.NoError(t, err)
	assert.Equal(t, Created, outcome, "the push's row is the scanner's output 0, not output 2")

	outcome, err = Record(context.Background(), store, deposit(models.TxSourceChain, 0, "bc1alice", "", "5000"), siblings)
	require.NoError(t, err)
	assert.Equal(t, SamePosition, outcome)
	assert.Equal(t, 2, store.count())
}

func TestRecord_Skips_TheOtherSourcesRowWhenThisSourceHasAnotherTransferAtItsPosition(t *testing.T) {
	store := &memoryStore{}
	require.Equal(t, Created, record(t, store, deposit(models.TxSourceDepositIngest, 30, "owner", "mint", "2200")))
	// The scanner's transfer at 30 is another one: the sources number the transaction
	// differently, and the pushed row is the scanner's transfer at 28.
	siblings := []Transfer{
		{Position: 28, To: "owner", Contract: "mint", Amount: "2200"},
		{Position: 30, To: "other", Contract: "mint", Amount: "7"},
	}

	outcome, err := Record(context.Background(), store, deposit(models.TxSourceChain, 28, "owner", "mint", "2200"), siblings)
	require.NoError(t, err)
	assert.Equal(t, OtherSource, outcome)
	assert.Equal(t, 1, store.count())
}

func TestRecord_Takes_TheTransactionLockForEveryDecision(t *testing.T) {
	store := &memoryStore{}
	record(t, store, deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10"))
	record(t, store, deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10"))

	assert.Equal(t, 2, store.locks)
}

func TestRecord_Inserts_IdenticalTransfersOfOneSourceAtTheirPositions(t *testing.T) {
	store := &memoryStore{}
	require.Equal(t, Created, record(t, store, deposit(models.TxSourceDepositIngest, 3, "0xalice", testUSDT, "10")))

	assert.Equal(t, Created, record(t, store, deposit(models.TxSourceDepositIngest, 9, "0xalice", testUSDT, "10")))
	assert.Equal(t, 2, store.count())
}

func TestRecord_Treats_AUniqueViolationOnInsertAsRecorded(t *testing.T) {
	store := &memoryStore{createErr: uniqueViolation()}

	outcome, err := Record(context.Background(), store, deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10"), nil)
	require.NoError(t, err)
	assert.Equal(t, Concurrent, outcome)
}

func TestRecord_Returns_OtherInsertFailures(t *testing.T) {
	boom := errors.New("connection refused")
	store := &memoryStore{createErr: boom}

	_, err := Record(context.Background(), store, deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10"), nil)
	assert.ErrorIs(t, err, boom)
}

func TestRecord_Returns_ALockFailureWithoutDeciding(t *testing.T) {
	boom := errors.New("connection refused")
	store := &memoryStore{lockErr: boom}

	_, err := Record(context.Background(), store, deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10"), nil)
	assert.ErrorIs(t, err, boom)
	assert.Zero(t, store.creates, "without the lock the scanner and the push could both insert")
}

func TestRecord_Returns_ALookupFailureWithoutInserting(t *testing.T) {
	boom := errors.New("connection refused")
	store := &memoryStore{findErr: boom}

	_, err := Record(context.Background(), store, deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10"), nil)
	assert.ErrorIs(t, err, boom)
	assert.Zero(t, store.creates)
}

func TestRecord_Decides_AgainWhenAnotherProcessClaimedTheOldRowFirst(t *testing.T) {
	store := &memoryStore{}
	legacy := deposit(models.TxSourceChain, types.NativeValuePosition, "0xalice", testUSDT, "10")
	store.rows = append(store.rows, *legacy)
	store.beforeClaim = func(m *memoryStore) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.rows[0].LogIndex = 3 // the other scan of the same transfer moved it
	}

	assert.Equal(t, SamePosition, record(t, store, deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10")))
	assert.Equal(t, 1, store.count())
}

func TestRecord_Treats_AClaimOntoATakenPositionAsRecorded(t *testing.T) {
	store := &memoryStore{}
	legacy := deposit(models.TxSourceChain, types.NativeValuePosition, "0xalice", testUSDT, "10")
	store.rows = append(store.rows, *legacy)
	store.beforeClaim = func(m *memoryStore) {
		m.mu.Lock()
		defer m.mu.Unlock()
		row := deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10")
		m.rows = append(m.rows, *row)
	}

	assert.Equal(t, Concurrent, record(t, store, deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10")))
}

func TestRecord_Concurrent_ScansOfOneTransferKeepOneRow(t *testing.T) {
	store := &memoryStore{}
	const scans = 16
	start := make(chan struct{})
	outcomes := make(chan Outcome, scans)
	var wg sync.WaitGroup
	for i := 0; i < scans; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			outcome, err := Record(context.Background(), store, deposit(models.TxSourceChain, 3, "0xalice", testUSDT, "10"), nil)
			assert.NoError(t, err)
			outcomes <- outcome
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	created := 0
	for outcome := range outcomes {
		if outcome == Created {
			created++
		}
	}
	assert.Equal(t, 1, created)
	assert.Equal(t, 1, store.count())
}
