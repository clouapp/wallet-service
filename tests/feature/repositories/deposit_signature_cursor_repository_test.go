package repositories_test

import (
	"context"
	"testing"

	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const (
	cursorOwner        = "AJor34TKjdm2pAK6V7nkBmKNpnARHL7CkiwnBVTE9J6b"
	cursorTokenAccount = "J97xc3FEjkZ9jXRAUjXaqK1r4j16u6rpYzsV8YA4HGaj"
)

type DepositSignatureCursorRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.DepositSignatureCursorRepository
}

func TestDeposit_Signature_CursorRepositorySuite(t *testing.T) {
	suite.Run(t, new(DepositSignatureCursorRepositoryTestSuite))
}

func (s *DepositSignatureCursorRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewDepositSignatureCursorRepository(nil)
}

func (s *DepositSignatureCursorRepositoryTestSuite) cursorsOf(chainID string) map[string]models.DepositSignatureCursor {
	rows, err := s.repo.FindByChain(context.Background(), chainID)
	s.Require().NoError(err)
	byAccount := map[string]models.DepositSignatureCursor{}
	for _, row := range rows {
		byAccount[row.Account] = row
	}
	return byAccount
}

func (s *DepositSignatureCursorRepositoryTestSuite) TestFind_ByChain_OfAChainNeverSyncedIsEmpty() {
	rows, err := s.repo.FindByChain(context.Background(), models.ChainTSOL)
	s.Require().NoError(err)
	s.Empty(rows)
}

func (s *DepositSignatureCursorRepositoryTestSuite) TestAdvance_Creates_ARowPerAccountOfAChain() {
	ctx := context.Background()
	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainTSOL, Account: cursorOwner, Signature: "sig-a", Slot: 100, SyncedFromSlot: 90}))
	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainTSOL, Account: cursorTokenAccount, Slot: 90, SyncedFromSlot: 90}))
	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainSOL, Account: cursorOwner, Signature: "sig-mainnet", Slot: 7}))

	devnet := s.cursorsOf(models.ChainTSOL)
	s.Len(devnet, 2, "the cursors of one chain only")
	s.Equal("sig-a", devnet[cursorOwner].Signature)
	s.EqualValues(100, devnet[cursorOwner].Slot)
	s.EqualValues(90, devnet[cursorOwner].SyncedFromSlot)
	s.Empty(devnet[cursorTokenAccount].Signature, "an account read without a signature yet keeps its floor")
	s.EqualValues(90, devnet[cursorTokenAccount].Slot)
	s.Equal("sig-mainnet", s.cursorsOf(models.ChainSOL)[cursorOwner].Signature)
}

func (s *DepositSignatureCursorRepositoryTestSuite) TestAdvance_MovesForwardAndKeepsTheSlotTheHistoryWasFirstReadFrom() {
	ctx := context.Background()
	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainTSOL, Account: cursorOwner, Slot: 90, SyncedFromSlot: 90}))

	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainTSOL, Account: cursorOwner, Signature: "sig-b", Slot: 120, SyncedFromSlot: 120}))
	moved := s.cursorsOf(models.ChainTSOL)[cursorOwner]
	s.Equal("sig-b", moved.Signature)
	s.EqualValues(120, moved.Slot)
	s.EqualValues(90, moved.SyncedFromSlot, "the slot the history was first read from stays")

	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainTSOL, Account: cursorOwner, Signature: "sig-c", Slot: 120}))
	s.Equal("sig-c", s.cursorsOf(models.ChainTSOL)[cursorOwner].Signature, "a later signature of the same slot moves the cursor")
}

func (s *DepositSignatureCursorRepositoryTestSuite) TestAdvance_MovesTheBackfillDownAndACoveredAccountStaysCovered() {
	ctx := context.Background()
	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainTSOL, Account: cursorOwner,
		Signature: "sig-top", Slot: 200, BackfillBefore: "sig-150", SyncedFromSlot: 100}))
	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainTSOL, Account: cursorOwner,
		Signature: "sig-top", Slot: 200, BackfillBefore: "sig-101", SyncedFromSlot: 100, Covered: true}))
	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainTSOL, Account: cursorOwner,
		Signature: "sig-new", Slot: 210, BackfillBefore: "sig-101", SyncedFromSlot: 100}))

	row := s.cursorsOf(models.ChainTSOL)[cursorOwner]
	s.Equal("sig-new", row.Signature)
	s.Equal("sig-101", row.BackfillBefore, "the backfill moves down with the same newest signature")
	s.True(row.Covered, "an account read down to its floor stays so")
}

// A burst leaves a stretch recorded ahead of a gap above the cursor: it is stored
// with the cursor, and cleared once the gap is read and the stretches join; the end of
// a history seen once is stored, and forgotten when the next read contradicts it.
func (s *DepositSignatureCursorRepositoryTestSuite) TestAdvance_KeepsTheStretchAheadOfAGapAndTheEndSeen() {
	ctx := context.Background()
	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainTSOL, Account: cursorOwner,
		Signature: "sig-top", Slot: 200, BackfillBefore: "sig-150", SyncedFromSlot: 100, EndSeen: true,
		AheadSignature: "sig-900", AheadSlot: 900, AheadBefore: "sig-800"}))

	row := s.cursorsOf(models.ChainTSOL)[cursorOwner]
	s.True(row.EndSeen)
	s.Equal("sig-900", row.AheadSignature)
	s.EqualValues(900, row.AheadSlot)
	s.Equal("sig-800", row.AheadBefore)

	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainTSOL, Account: cursorOwner,
		Signature: "sig-900", Slot: 900, BackfillBefore: "sig-150", SyncedFromSlot: 100}))

	row = s.cursorsOf(models.ChainTSOL)[cursorOwner]
	s.Equal("sig-900", row.Signature, "the stretches joined")
	s.Empty(row.AheadSignature)
	s.Zero(row.AheadSlot)
	s.Empty(row.AheadBefore)
	s.False(row.EndSeen, "an end the next read contradicted is forgotten")
}

// The anchors of the two stretch ends (the signatures recorded right above them) are
// stored and move with them.
func (s *DepositSignatureCursorRepositoryTestSuite) TestAdvance_KeepsTheAnchorOfEachStretchEnd() {
	ctx := context.Background()
	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainTSOL, Account: cursorOwner,
		Signature: "sig-top", Slot: 200, BackfillBefore: "sig-150", BackfillAnchor: "sig-151", SyncedFromSlot: 100,
		AheadSignature: "sig-900", AheadSlot: 900, AheadBefore: "sig-800", AheadAnchor: "sig-801"}))

	row := s.cursorsOf(models.ChainTSOL)[cursorOwner]
	s.Equal("sig-151", row.BackfillAnchor)
	s.Equal("sig-801", row.AheadAnchor)

	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainTSOL, Account: cursorOwner,
		Signature: "sig-900", Slot: 900, BackfillBefore: "sig-120", BackfillAnchor: "sig-121", SyncedFromSlot: 100}))

	row = s.cursorsOf(models.ChainTSOL)[cursorOwner]
	s.Equal("sig-120", row.BackfillBefore)
	s.Equal("sig-121", row.BackfillAnchor, "the anchor moves down with the oldest recorded signature")
	s.Empty(row.AheadAnchor, "the stretches joined: no anchor ahead")
}

func (s *DepositSignatureCursorRepositoryTestSuite) TestAdvance_Never_MovesACursorBack() {
	ctx := context.Background()
	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainTSOL, Account: cursorOwner, Signature: "sig-new", Slot: 500}))

	s.Require().NoError(s.repo.Advance(ctx, models.DepositSignatureCursor{Chain: models.ChainTSOL, Account: cursorOwner, Signature: "sig-old", Slot: 499}))

	kept := s.cursorsOf(models.ChainTSOL)[cursorOwner]
	s.Equal("sig-new", kept.Signature, "a slower sync must not rewind the account")
	s.EqualValues(500, kept.Slot)
}

func (s *DepositSignatureCursorRepositoryTestSuite) TestAdvance_Refuses_ABlankKeyOrANegativeSlot() {
	ctx := context.Background()
	for name, cursor := range map[string]models.DepositSignatureCursor{
		"blank chain":          {Account: cursorOwner, Slot: 1},
		"blank account":        {Chain: models.ChainTSOL, Account: "  ", Slot: 1},
		"negative slot":        {Chain: models.ChainTSOL, Account: cursorOwner, Slot: -1},
		"negative synced slot": {Chain: models.ChainTSOL, Account: cursorOwner, SyncedFromSlot: -1},
		"negative ahead slot":  {Chain: models.ChainTSOL, Account: cursorOwner, AheadSlot: -1},
	} {
		s.Run(name, func() {
			s.Error(s.repo.Advance(ctx, cursor))
		})
	}
	s.Empty(s.cursorsOf(models.ChainTSOL))
}

func (s *DepositSignatureCursorRepositoryTestSuite) TestFind_ByChain_RefusesANegativeStoredSlot() {
	_, err := facades.Orm().Query().Exec(`INSERT INTO deposit_signature_cursors (chain, account, signature, slot, synced_from_slot, created_at, updated_at)
		VALUES (?, ?, '', -5, 0, NOW(), NOW())`, models.ChainTSOL, cursorOwner)
	s.Require().NoError(err)

	_, err = s.repo.FindByChain(context.Background(), models.ChainTSOL)
	s.Error(err)
}
