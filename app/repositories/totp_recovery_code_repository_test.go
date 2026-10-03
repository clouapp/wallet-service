package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type TotpRecoveryCodeRepositoryTestSuite struct {
	suite.Suite
	repo     *repositories.TotpRecoveryCodeRepository
	userRepo *repositories.UserRepository
}

func TestTotpRecoveryCodeRepositorySuite(t *testing.T) {
	suite.Run(t, new(TotpRecoveryCodeRepositoryTestSuite))
}

func (s *TotpRecoveryCodeRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewTotpRecoveryCodeRepository(nil)
	s.userRepo = repositories.NewUserRepository(nil)
}

func (s *TotpRecoveryCodeRepositoryTestSuite) createUser() uuid.UUID {
	u := &models.User{ID: uuid.New(), Email: uuid.NewString() + "@test.com", PasswordHash: "h", Status: "active"}
	s.Require().NoError(s.userRepo.Create(context.Background(), u))
	return u.ID
}

func (s *TotpRecoveryCodeRepositoryTestSuite) TestFindUnusedByUserID() {
	userID := s.createUser()

	unused := &models.TotpRecoveryCode{ID: uuid.New(), UserID: userID, CodeHash: "unused_hash"}
	facades.Orm().Query().Create(unused)

	used := &models.TotpRecoveryCode{ID: uuid.New(), UserID: userID, CodeHash: "used_hash"}
	facades.Orm().Query().Create(used)
	now := time.Now()
	facades.Orm().Query().Model(used).Where("id = ?", used.ID).Update("used_at", now)

	codes, err := s.repo.FindUnusedByUserID(context.Background(), userID)
	s.NoError(err)
	s.Len(codes, 1)
	s.Equal(unused.ID, codes[0].ID)
}

func (s *TotpRecoveryCodeRepositoryTestSuite) TestMarkUsedIfUnused() {
	userID := insertActiveUserRow(s.T())

	code := &models.TotpRecoveryCode{ID: uuid.New(), UserID: userID, CodeHash: "hash"}
	s.Require().NoError(facades.Orm().Query().Create(code))

	spent, err := s.repo.MarkUsedIfUnused(context.Background(), code.ID)
	s.NoError(err)
	s.True(spent)

	var check models.TotpRecoveryCode
	facades.Orm().Query().Where("id = ?", code.ID).First(&check)
	s.NotNil(check.UsedAt)
}

func (s *TotpRecoveryCodeRepositoryTestSuite) TestMarkUsedIfUnused_SecondSpendIsRefused() {
	userID := insertActiveUserRow(s.T())

	code := &models.TotpRecoveryCode{ID: uuid.New(), UserID: userID, CodeHash: "hash"}
	s.Require().NoError(facades.Orm().Query().Create(code))

	first, err := s.repo.MarkUsedIfUnused(context.Background(), code.ID)
	s.Require().NoError(err)
	s.Require().True(first)

	second, err := s.repo.MarkUsedIfUnused(context.Background(), code.ID)
	s.NoError(err)
	s.False(second, "a spent recovery code must not be spendable again")
}
