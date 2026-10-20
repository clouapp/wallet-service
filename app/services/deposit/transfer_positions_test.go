package deposit

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/depositrecord"
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	positionsChain     = "eth"
	positionsBlock     = 200
	positionsBlockHash = "0xblock200"
	positionsHead      = 210
	positionsTx        = "0xbatchpayment"
	positionsUSDT      = "0xdac17f958d2ee523a2206206994597c13d831ec7"
)

var positionsToken = types.Token{Symbol: models.SymbolUSDT, Contract: positionsUSDT, Decimals: 6, ChainID: positionsChain}

// publishedDeposits records which transaction ids each deposit event was published for.
type publishedDeposits struct {
	mu  sync.Mutex
	ids map[types.EventType][]uuid.UUID
}

func (p *publishedDeposits) Publish(_ context.Context, eventType types.EventType, tx models.Transaction) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ids == nil {
		p.ids = map[types.EventType][]uuid.UUID{}
	}
	p.ids[eventType] = append(p.ids[eventType], tx.ID)
	return nil
}

func (p *publishedDeposits) Republish(ctx context.Context, eventType types.EventType, tx models.Transaction, _ time.Time) error {
	return p.Publish(ctx, eventType, tx)
}

func (p *publishedDeposits) pending() []uuid.UUID {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]uuid.UUID(nil), p.ids[types.EventDepositPending]...)
}

type positionsFixture struct {
	svc      *Service
	adapter  *mocks.MockChain
	registry *chain.Registry
	events   *publishedDeposits
	blocks   map[uint64][]types.DetectedTransfer
}

func newPositionsFixture(t *testing.T, chainID, nativeAsset string) *positionsFixture {
	t.Helper()
	fixtures.TestDB(t)
	f := &positionsFixture{blocks: map[uint64][]types.DetectedTransfer{}, events: &publishedDeposits{}}
	f.adapter = mocks.NewMockChain(chainID)
	f.adapter.NativeAssetVal = nativeAsset
	f.adapter.RequiredConfirmationsVal = 3
	f.adapter.GetLatestBlockFn = func(context.Context) (uint64, error) { return positionsHead, nil }
	f.adapter.ScanBlockFn = func(_ context.Context, block uint64) ([]types.DetectedTransfer, error) {
		return f.blocks[block], nil
	}
	f.adapter.GetTransactionBlockFn = func(_ context.Context, txHash string) (uint64, error) {
		for block, transfers := range f.blocks {
			for _, transfer := range transfers {
				if transfer.TxHash == txHash {
					return block, nil
				}
			}
		}
		return 0, nil
	}
	f.registry = chain.NewRegistry()
	f.registry.RegisterChain(f.adapter)
	f.registry.RegisterToken(positionsToken)
	f.svc = newDepositSvc(f.registry, newWebhookSvc())
	f.svc.SetDepositEvents(f.events)
	return f
}

// watch is a watched address of its own wallet.
func (f *positionsFixture) watch(t *testing.T, chainID, address string) models.Address {
	t.Helper()
	wallet := fixtures.InsertWallet(t, chainID)
	return fixtures.InsertAddress(t, wallet.ID, chainID, address, "user-"+address, 0)
}

func (f *positionsFixture) scan(t *testing.T, chainID string, block uint64) int {
	t.Helper()
	recorded, err := f.svc.ScanBlock(context.Background(), chainID, block)
	require.NoError(t, err)
	return recorded
}

// tokenLog is an ERC-20 Transfer log of positionsTx in positionsBlock.
func tokenLog(to string, amount int64, logIndex int) types.DetectedTransfer {
	token := positionsToken
	return types.DetectedTransfer{
		TxHash: positionsTx, BlockNumber: positionsBlock, BlockHash: positionsBlockHash,
		From: "0xpayer", To: to, Amount: big.NewInt(amount), Asset: token.Symbol, Token: &token, LogIndex: logIndex,
	}
}

func depositsOf(t *testing.T, chainID, txHash string) []models.Transaction {
	t.Helper()
	var rows []models.Transaction
	require.NoError(t, facades.Orm().Query().
		Where("chain", chainID).Where("tx_hash", txHash).Where("tx_type", models.TxTypeDeposit).
		Order("log_index").Find(&rows))
	return rows
}

func TestScan_Block_RecordsATransactionThatPaysTwoWatchedAddressesAsTwoDeposits(t *testing.T) {
	f := newPositionsFixture(t, positionsChain, models.NativeETH)
	alice := f.watch(t, positionsChain, "0xalice")
	bob := f.watch(t, positionsChain, "0xbob")
	f.blocks[positionsBlock] = []types.DetectedTransfer{
		tokenLog(alice.Address, 10_000_000, 3),
		tokenLog("0xsomeoneelse", 5_000_000, 4),
		tokenLog(bob.Address, 20_000_000, 7),
	}

	assert.Equal(t, 2, f.scan(t, positionsChain, positionsBlock))

	rows := depositsOf(t, positionsChain, positionsTx)
	require.Len(t, rows, 2)
	assert.Equal(t, alice.Address, rows[0].ToAddress)
	assert.Equal(t, 3, rows[0].LogIndex)
	assert.Equal(t, alice.WalletID, rows[0].WalletID)
	assert.Equal(t, "10000000", rows[0].Amount)
	assert.Equal(t, bob.Address, rows[1].ToAddress)
	assert.Equal(t, 7, rows[1].LogIndex)
	assert.Equal(t, bob.WalletID, rows[1].WalletID)
	assert.NotEqual(t, rows[0].ID, rows[1].ID, "each deposit has its own transaction id, the key of its webhooks")
	assert.ElementsMatch(t, []uuid.UUID{rows[0].ID, rows[1].ID}, f.events.pending(), "deposit.pending once per deposit")
}

func TestScan_Block_RecordsTwoTokenLogsToOneAddressAsTwoDeposits(t *testing.T) {
	f := newPositionsFixture(t, positionsChain, models.NativeETH)
	alice := f.watch(t, positionsChain, "0xalice")
	f.blocks[positionsBlock] = []types.DetectedTransfer{
		tokenLog(alice.Address, 10_000_000, 0),
		tokenLog(alice.Address, 10_000_000, 1),
	}

	assert.Equal(t, 2, f.scan(t, positionsChain, positionsBlock))

	rows := depositsOf(t, positionsChain, positionsTx)
	require.Len(t, rows, 2)
	assert.Equal(t, 0, rows[0].LogIndex, "the first log of a block is position 0, not the column default")
	assert.Equal(t, 1, rows[1].LogIndex)
	assert.Len(t, f.events.pending(), 2)
}

func TestScan_Block_RecordsTheNativeValueBesideTheTokenLogsOfOneTransaction(t *testing.T) {
	f := newPositionsFixture(t, positionsChain, models.NativeETH)
	alice := f.watch(t, positionsChain, "0xalice")
	f.blocks[positionsBlock] = []types.DetectedTransfer{
		{TxHash: positionsTx, BlockNumber: positionsBlock, BlockHash: positionsBlockHash, From: "0xpayer", To: alice.Address,
			Amount: big.NewInt(1_000_000_000_000_000), Asset: models.NativeETH, LogIndex: types.NativeValuePosition},
		tokenLog(alice.Address, 10_000_000, 0),
	}

	assert.Equal(t, 2, f.scan(t, positionsChain, positionsBlock))

	rows := depositsOf(t, positionsChain, positionsTx)
	require.Len(t, rows, 2)
	assert.Equal(t, types.NativeValuePosition, rows[0].LogIndex)
	assert.Empty(t, rows[0].TokenContract)
	assert.Equal(t, 0, rows[1].LogIndex)
	assert.Equal(t, positionsUSDT, rows[1].TokenContract)
}

func TestScan_Block_RecordsTwoBitcoinOutputsToWatchedAddresses(t *testing.T) {
	const btcTx = "4a5e1e4baab89f3a32518a88c31bc87f618f76673e2cc77ab2127b7afdeda33b"
	f := newPositionsFixture(t, models.ChainBTC, models.NativeBTC)
	first := f.watch(t, models.ChainBTC, "bc1qfirst")
	second := f.watch(t, models.ChainBTC, "bc1qsecond")
	output := func(to string, sats int64, vout int) types.DetectedTransfer {
		return types.DetectedTransfer{TxHash: btcTx, BlockNumber: positionsBlock, BlockHash: "00000000block",
			To: to, Amount: big.NewInt(sats), Asset: models.NativeBTC, LogIndex: vout}
	}
	f.blocks[positionsBlock] = []types.DetectedTransfer{
		output(first.Address, 50_000, 0),
		output("bc1qchange", 10_000, 1),
		output(second.Address, 70_000, 2),
		output(first.Address, 50_000, 3),
	}

	assert.Equal(t, 3, f.scan(t, models.ChainBTC, positionsBlock))

	rows := depositsOf(t, models.ChainBTC, btcTx)
	require.Len(t, rows, 3)
	positions := []int{rows[0].LogIndex, rows[1].LogIndex, rows[2].LogIndex}
	assert.Equal(t, []int{0, 2, 3}, positions, "one deposit per output, by its vout")
}

func TestScan_Block_ScanningATransactionAgainRecordsNothingNew(t *testing.T) {
	f := newPositionsFixture(t, positionsChain, models.NativeETH)
	alice := f.watch(t, positionsChain, "0xalice")
	bob := f.watch(t, positionsChain, "0xbob")
	f.blocks[positionsBlock] = []types.DetectedTransfer{
		tokenLog(alice.Address, 10_000_000, 3),
		tokenLog(bob.Address, 20_000_000, 7),
	}
	require.Equal(t, 2, f.scan(t, positionsChain, positionsBlock))
	before := depositsOf(t, positionsChain, positionsTx)

	assert.Equal(t, 0, f.scan(t, positionsChain, positionsBlock))
	recorded, err := f.svc.ScanTransaction(context.Background(), positionsChain, positionsTx)
	require.NoError(t, err)
	assert.Equal(t, 0, recorded)

	after := depositsOf(t, positionsChain, positionsTx)
	require.Len(t, after, 2)
	assert.Equal(t, before[0].ID, after[0].ID)
	assert.Equal(t, before[1].ID, after[1].ID)
	assert.Len(t, f.events.pending(), 2, "no deposit.pending is sent again")
}

// insertScannerRowBeforePositions writes a deposit of positionsBlock the way the
// scanner recorded one before transfer positions: log_index -1, whatever the transfer was.
func insertScannerRowBeforePositions(t *testing.T, address models.Address, transfer types.DetectedTransfer) models.Transaction {
	t.Helper()
	contract := ""
	asset := models.NativeETH
	if transfer.Token != nil {
		contract, asset = transfer.Token.Contract, transfer.Token.Symbol
	}
	row := models.Transaction{
		ID: uuid.New(), AddressID: &address.ID, WalletID: address.WalletID, ExternalUserID: address.ExternalUserID,
		Chain: address.Chain, TxType: models.TxTypeDeposit, TxHash: transfer.TxHash, LogIndex: types.NativeValuePosition,
		FromAddress: transfer.From, ToAddress: transfer.To, Amount: transfer.Amount.String(), Asset: asset, TokenContract: contract,
		RequiredConfs: 3, Status: "confirmed", BlockNumber: positionsBlock, BlockHash: transfer.BlockHash,
		Direction: models.TxDirectionInbound, Source: models.TxSourceChain, RawPayload: "{}",
	}
	require.NoError(t, facades.Orm().Query().Create(&row))
	return row
}

func TestScan_Block_MovesTheOldRowOfATransactionToItsPositionAndRecordsWhatItLeftOut(t *testing.T) {
	f := newPositionsFixture(t, positionsChain, models.NativeETH)
	alice := f.watch(t, positionsChain, "0xalice")
	bob := f.watch(t, positionsChain, "0xbob")
	first, second := tokenLog(alice.Address, 10_000_000, 3), tokenLog(bob.Address, 20_000_000, 7)
	old := insertScannerRowBeforePositions(t, alice, first)
	f.blocks[positionsBlock] = []types.DetectedTransfer{first, second}

	assert.Equal(t, 1, f.scan(t, positionsChain, positionsBlock), "only the transfer the old rule left out is new")

	rows := depositsOf(t, positionsChain, positionsTx)
	require.Len(t, rows, 2)
	assert.Equal(t, old.ID, rows[0].ID, "the delivered deposit keeps its transaction id")
	assert.Equal(t, 3, rows[0].LogIndex)
	assert.Equal(t, "confirmed", rows[0].Status)
	assert.Equal(t, bob.Address, rows[1].ToAddress)
	assert.Equal(t, []uuid.UUID{rows[1].ID}, f.events.pending(), "no event is sent again for the old deposit")

	assert.Equal(t, 0, f.scan(t, positionsChain, positionsBlock))
	assert.Len(t, depositsOf(t, positionsChain, positionsTx), 2)
}

func TestScan_Block_ATransactionMinedAgainAfterAReorganizationKeepsOneDeposit(t *testing.T) {
	f := newPositionsFixture(t, positionsChain, models.NativeETH)
	alice := f.watch(t, positionsChain, "0xalice")
	f.blocks[positionsBlock] = []types.DetectedTransfer{tokenLog(alice.Address, 10_000_000, 3)}
	require.Equal(t, 1, f.scan(t, positionsChain, positionsBlock))

	reincluded := tokenLog(alice.Address, 10_000_000, 12)
	reincluded.BlockNumber, reincluded.BlockHash = positionsBlock+1, "0xblock201"
	f.blocks[positionsBlock+1] = []types.DetectedTransfer{reincluded}

	assert.Equal(t, 0, f.scan(t, positionsChain, positionsBlock+1))
	assert.Len(t, depositsOf(t, positionsChain, positionsTx), 1)
}

// newIngestService is the provider push path on the same database and registry.
func (f *positionsFixture) newIngestService() *ingest.Service {
	svc := newIngestSvc(f.registry)
	svc.SetDepositEvents(f.events)
	return svc
}

// pushedTokenLog is how Alchemy reports the same Transfer log: its log index, the
// raw value in base units.
func pushedTokenLog(to string, amount int64, logIndex int) providers.InboundTransfer {
	return providers.InboundTransfer{
		TxHash: positionsTx, BlockNumber: positionsBlock, BlockHash: positionsBlockHash,
		From: "0xpayer", To: to, Amount: big.NewInt(amount), Asset: "USDT",
		Token: &types.Token{Contract: positionsUSDT, Decimals: 6}, LogIndex: logIndex,
	}
}

func TestPush_And_Scan_RecordOneDepositForOneTransfer(t *testing.T) {
	for _, pushFirst := range []bool{true, false} {
		name := "scanner first"
		if pushFirst {
			name = "push first"
		}
		t.Run(name, func(t *testing.T) {
			f := newPositionsFixture(t, positionsChain, models.NativeETH)
			alice := f.watch(t, positionsChain, "0xalice")
			bob := f.watch(t, positionsChain, "0xbob")
			native := types.DetectedTransfer{TxHash: positionsTx, BlockNumber: positionsBlock, BlockHash: positionsBlockHash,
				From: "0xpayer", To: bob.Address, Amount: big.NewInt(7_000), Asset: models.NativeETH, LogIndex: types.NativeValuePosition}
			f.blocks[positionsBlock] = []types.DetectedTransfer{native, tokenLog(alice.Address, 10_000_000, 0), tokenLog(bob.Address, 20_000_000, 5)}
			push := []providers.InboundTransfer{
				pushedTokenLog(alice.Address, 10_000_000, 0),
				pushedTokenLog(bob.Address, 20_000_000, 5),
				// An external transfer has no log; Alchemy sends no block hash for it.
				{TxHash: positionsTx, BlockNumber: positionsBlock, From: "0xpayer", To: bob.Address,
					Amount: big.NewInt(7_000), Asset: "ETH", LogIndex: types.NativeValuePosition},
			}
			ingestSvc := f.newIngestService()
			pushOnce := func() {
				require.NoError(t, ingestSvc.Ingest(context.Background(), ingest.InboundEvent{ChainID: positionsChain, Transfers: push}))
			}

			if pushFirst {
				pushOnce()
				for _, row := range depositsOf(t, positionsChain, positionsTx) {
					assert.Equal(t, models.TxSourceDepositIngest, row.Source)
					assert.Equal(t, models.TxDirectionInbound, row.Direction)
				}
				assert.Equal(t, 0, f.scan(t, positionsChain, positionsBlock))
			} else {
				assert.Equal(t, 3, f.scan(t, positionsChain, positionsBlock))
				pushOnce()
			}
			pushOnce()

			rows := depositsOf(t, positionsChain, positionsTx)
			require.Len(t, rows, 3)
			assert.Equal(t, []int{types.NativeValuePosition, 0, 5}, []int{rows[0].LogIndex, rows[1].LogIndex, rows[2].LogIndex})
			assert.Len(t, f.events.pending(), 3, "deposit.pending once per deposit, whichever source saw it first")
		})
	}
}

func TestPush_Of_ATransferTheScannerRecordedBeforePositionsIsNotRecordedAgain(t *testing.T) {
	f := newPositionsFixture(t, positionsChain, models.NativeETH)
	alice := f.watch(t, positionsChain, "0xalice")
	old := insertScannerRowBeforePositions(t, alice, tokenLog(alice.Address, 10_000_000, 3))

	require.NoError(t, f.newIngestService().Ingest(context.Background(), ingest.InboundEvent{
		ChainID: positionsChain, Transfers: []providers.InboundTransfer{pushedTokenLog(alice.Address, 10_000_000, 3)},
	}))

	rows := depositsOf(t, positionsChain, positionsTx)
	require.Len(t, rows, 1)
	assert.Equal(t, old.ID, rows[0].ID)
	assert.Equal(t, types.NativeValuePosition, rows[0].LogIndex, "only the scanner moves its own old row, on its next scan")
	assert.Empty(t, f.events.pending())
}

// The scanner and the push record one transfer at the same moment, numbering it
// differently (the push listed the transaction's accounts in another order): the
// transaction lock makes one of them see the other's row.
func TestRecord_ConcurrentScanAndPushOfOneTransferKeepOneRow(t *testing.T) {
	f := newPositionsFixture(t, positionsChain, models.NativeETH)
	alice := f.watch(t, positionsChain, "0xalice")
	transactions := repositoryOf(t)
	for attempt := 0; attempt < 10; attempt++ {
		txHash := fmt.Sprintf("0xrace%02d", attempt)
		row := func(source string, position int) *models.Transaction {
			return &models.Transaction{
				ID: uuid.New(), AddressID: &alice.ID, WalletID: alice.WalletID, ExternalUserID: alice.ExternalUserID,
				Chain: positionsChain, TxType: models.TxTypeDeposit, TxHash: txHash, LogIndex: position,
				ToAddress: alice.Address, Amount: "10000000", Asset: models.SymbolUSDT, TokenContract: positionsUSDT,
				RequiredConfs: 3, Status: "pending", BlockNumber: positionsBlock, BlockHash: positionsBlockHash,
				Direction: models.TxDirectionInbound, Source: source, RawPayload: "{}",
			}
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		outcomes := make([]depositrecord.Outcome, 2)
		for i, candidate := range []*models.Transaction{row(models.TxSourceChain, 3), row(models.TxSourceDepositIngest, 5)} {
			wg.Add(1)
			go func(i int, candidate *models.Transaction) {
				defer wg.Done()
				<-start
				outcome, err := depositrecord.Record(context.Background(), transactions, candidate, nil)
				assert.NoError(t, err)
				outcomes[i] = outcome
			}(i, candidate)
		}
		close(start)
		wg.Wait()

		require.Len(t, depositsOf(t, positionsChain, txHash), 1, "attempt %d: outcomes %v", attempt, outcomes)
		assert.ElementsMatch(t, []depositrecord.Outcome{depositrecord.Created, depositrecord.OtherSource}, outcomes)
	}
}

// Outputs 0 and 2 pay alice the same amount; the push recorded output 0 and stopped.
// The scanner reports both, so the pushed row is its output 0 and output 2 is new.
func TestScan_Block_RecordsTheTwinOutputThePushLeftOut(t *testing.T) {
	const btcTx = "9b0fc92260312ce44e74ef369f5c66bbb85848f2eddd5a7a1cde251e54ccfdd5"
	f := newPositionsFixture(t, models.ChainBTC, models.NativeBTC)
	alice := f.watch(t, models.ChainBTC, "bc1qalice")
	output := func(vout int) types.DetectedTransfer {
		return types.DetectedTransfer{TxHash: btcTx, BlockNumber: positionsBlock, BlockHash: "00000000block",
			To: alice.Address, Amount: big.NewInt(50_000), Asset: models.NativeBTC, LogIndex: vout}
	}
	require.NoError(t, f.newIngestService().Ingest(context.Background(), ingest.InboundEvent{
		ChainID: models.ChainBTC, Transfers: []providers.InboundTransfer{{
			TxHash: btcTx, BlockNumber: positionsBlock, BlockHash: "00000000block", To: alice.Address,
			Amount: big.NewInt(50_000), Asset: "BTC", LogIndex: 0,
		}},
	}))
	f.blocks[positionsBlock] = []types.DetectedTransfer{output(0), output(2)}

	assert.Equal(t, 1, f.scan(t, models.ChainBTC, positionsBlock))

	rows := depositsOf(t, models.ChainBTC, btcTx)
	require.Len(t, rows, 2)
	assert.Equal(t, []int{0, 2}, []int{rows[0].LogIndex, rows[1].LogIndex})
	assert.Equal(t, models.TxSourceDepositIngest, rows[0].Source)
	assert.Equal(t, models.TxSourceChain, rows[1].Source)
}

// moveToBlock is what the reorganization check does with a deposit whose
// transaction was mined again: the row takes the new block and keeps its log index.
func moveToBlock(t *testing.T, row models.Transaction, number uint64, hash string) {
	t.Helper()
	_, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id", row.ID).
		Update(map[string]any{"block_number": number, "block_hash": hash})
	require.NoError(t, err)
}

// A deposit moved to the block its transaction was mined in again keeps the log index
// of the old block; a scan of the new block, where the Transfer has another log index,
// is the same transfer.
func TestScan_Block_ADepositMovedToItsNewBlockIsNotRecordedAgainByAScanOfThatBlock(t *testing.T) {
	for name, newHeight := range map[string]uint64{"competing block at the same height": positionsBlock, "mined again later": positionsBlock + 1} {
		t.Run(name, func(t *testing.T) {
			f := newPositionsFixture(t, positionsChain, models.NativeETH)
			alice := f.watch(t, positionsChain, "0xalice")
			f.blocks[positionsBlock] = []types.DetectedTransfer{tokenLog(alice.Address, 10_000_000, 3)}
			require.Equal(t, 1, f.scan(t, positionsChain, positionsBlock))
			rows := depositsOf(t, positionsChain, positionsTx)
			require.Len(t, rows, 1)

			moveToBlock(t, rows[0], newHeight, "0xnewblock")
			again := tokenLog(alice.Address, 10_000_000, 12)
			again.BlockNumber, again.BlockHash = newHeight, "0xnewblock"
			f.blocks[positionsBlock] = nil
			f.blocks[newHeight] = []types.DetectedTransfer{again}

			assert.Equal(t, 0, f.scan(t, positionsChain, newHeight))
			recorded, err := f.svc.ScanTransaction(context.Background(), positionsChain, positionsTx)
			require.NoError(t, err)
			assert.Equal(t, 0, recorded)
			assert.Len(t, depositsOf(t, positionsChain, positionsTx), 1, "one transfer, one deposit")
		})
	}
}

// An old row the one-per-transaction rule kept for a token transfer, beside a
// native value it left out: one rescan records the native value too.
func TestScan_Block_OneRescanMovesAnOldTokenRowAndRecordsTheNativeValue(t *testing.T) {
	f := newPositionsFixture(t, positionsChain, models.NativeETH)
	alice := f.watch(t, positionsChain, "0xalice")
	bob := f.watch(t, positionsChain, "0xbob")
	native := types.DetectedTransfer{TxHash: positionsTx, BlockNumber: positionsBlock, BlockHash: positionsBlockHash,
		From: "0xpayer", To: bob.Address, Amount: big.NewInt(7_000), Asset: models.NativeETH, LogIndex: types.NativeValuePosition}
	token := tokenLog(alice.Address, 10_000_000, 3)
	old := insertScannerRowBeforePositions(t, alice, token)
	f.blocks[positionsBlock] = []types.DetectedTransfer{native, token}

	assert.Equal(t, 1, f.scan(t, positionsChain, positionsBlock), "the native value is recorded on the first rescan")

	rows := depositsOf(t, positionsChain, positionsTx)
	require.Len(t, rows, 2)
	assert.Equal(t, bob.Address, rows[0].ToAddress)
	assert.Equal(t, types.NativeValuePosition, rows[0].LogIndex)
	assert.Equal(t, old.ID, rows[1].ID)
	assert.Equal(t, 3, rows[1].LogIndex)
}

// A pushed native deposit has no block hash (Alchemy sends none without a log); the
// scanner's sight of it writes the hash the reorganization check reads.
func TestScan_Block_FillsTheBlockHashOfAPushedDeposit(t *testing.T) {
	f := newPositionsFixture(t, positionsChain, models.NativeETH)
	bob := f.watch(t, positionsChain, "0xbob")
	require.NoError(t, f.newIngestService().Ingest(context.Background(), ingest.InboundEvent{ChainID: positionsChain, Transfers: []providers.InboundTransfer{
		{TxHash: positionsTx, BlockNumber: positionsBlock, From: "0xpayer", To: bob.Address, Amount: big.NewInt(7_000), Asset: "ETH", LogIndex: types.NativeValuePosition},
	}}))
	f.blocks[positionsBlock] = []types.DetectedTransfer{{TxHash: positionsTx, BlockNumber: positionsBlock, BlockHash: positionsBlockHash,
		From: "0xpayer", To: bob.Address, Amount: big.NewInt(7_000), Asset: models.NativeETH, LogIndex: types.NativeValuePosition}}

	assert.Equal(t, 0, f.scan(t, positionsChain, positionsBlock))

	rows := depositsOf(t, positionsChain, positionsTx)
	require.Len(t, rows, 1)
	assert.Equal(t, positionsBlockHash, rows[0].BlockHash)
}

// Internal transfers (native value a contract call sends) have positions of their
// own, below the native value position, as the Alchemy adapter gives them.
func pushedNative(to string, amount int64, position int) providers.InboundTransfer {
	return providers.InboundTransfer{TxHash: positionsTx, BlockNumber: positionsBlock, From: "0xdisperse", To: to,
		Amount: big.NewInt(amount), Asset: "ETH", LogIndex: position}
}

func TestPush_Of_ABatchPayoutRecordsEachInternalTransfer(t *testing.T) {
	f := newPositionsFixture(t, positionsChain, models.NativeETH)
	alice := f.watch(t, positionsChain, "0xalice")
	bob := f.watch(t, positionsChain, "0xbob")

	require.NoError(t, f.newIngestService().Ingest(context.Background(), ingest.InboundEvent{ChainID: positionsChain, Transfers: []providers.InboundTransfer{
		pushedNative(alice.Address, 1_000, -7_001), pushedNative(bob.Address, 2_000, -91_244),
	}}))

	assert.Len(t, depositsOf(t, positionsChain, positionsTx), 2, "two users paid by one batch transaction")
}

func TestPush_And_Scan_RecordTheExternalAndTheInternalTransferOfOneTransaction(t *testing.T) {
	for name, internalFirst := range map[string]bool{"external first": false, "internal first": true} {
		t.Run(name, func(t *testing.T) {
			f := newPositionsFixture(t, positionsChain, models.NativeETH)
			alice := f.watch(t, positionsChain, "0xalice")
			bob := f.watch(t, positionsChain, "0xbob")
			external := pushedNative(bob.Address, 7_000, types.NativeValuePosition)
			internal := pushedNative(alice.Address, 9_000, -7_001)
			push := []providers.InboundTransfer{external, internal}
			if internalFirst {
				push = []providers.InboundTransfer{internal, external}
			}
			require.NoError(t, f.newIngestService().Ingest(context.Background(), ingest.InboundEvent{ChainID: positionsChain, Transfers: push}))
			f.blocks[positionsBlock] = []types.DetectedTransfer{{TxHash: positionsTx, BlockNumber: positionsBlock, BlockHash: positionsBlockHash,
				From: "0xpayer", To: bob.Address, Amount: big.NewInt(7_000), Asset: models.NativeETH, LogIndex: types.NativeValuePosition}}

			assert.Equal(t, 0, f.scan(t, positionsChain, positionsBlock))
			assert.Len(t, depositsOf(t, positionsChain, positionsTx), 2, "the scanner's native value is the pushed external transfer")
		})
	}
}
