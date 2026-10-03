package repositories_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	"github.com/macrowallets/waas/tests/mocks"
)

type AccountActivityRepositoryTestSuite struct {
	suite.Suite
	activity    *repositories.AccountActivityRepository
	memberships *repositories.AccountUserRepository
	settings    *repositories.SettingRepository
}

func TestAccountActivityRepositorySuite(t *testing.T) {
	suite.Run(t, new(AccountActivityRepositoryTestSuite))
}

func (s *AccountActivityRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.activity = repositories.NewAccountActivityRepository(nil)
	s.memberships = repositories.NewAccountUserRepository(nil)
	s.settings = repositories.NewSettingRepository(nil)
}

func (s *AccountActivityRepositoryTestSuite) TestMembershipAndActivityRollBackTogether() {
	ctx := context.Background()
	accountID := s.account()
	actorID := s.user()
	targetID := s.user()
	membershipID := s.membership(accountID, targetID, "user")

	err := s.memberships.Within(ctx, func(ctx context.Context) error {
		if err := s.memberships.SetRole(ctx, membershipID, "admin"); err != nil {
			return err
		}
		if err := s.appendMember(ctx, accountID, actorID, targetID, "admin"); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	s.Require().Error(err)

	role, status := s.storedMembership(accountID, targetID)
	s.Equal("user", role)
	s.Equal(models.MembershipStatusActive, status)
	rows, total, err := s.activity.List(ctx, accountID, 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(0), total)
	s.Empty(rows)
}

func (s *AccountActivityRepositoryTestSuite) TestMembershipAndActivityCommitTogether() {
	ctx := context.Background()
	accountID := s.account()
	actorID := s.user()
	targetID := s.user()
	membershipID := s.membership(accountID, targetID, "user")

	err := s.memberships.Within(ctx, func(ctx context.Context) error {
		if err := s.memberships.SetRole(ctx, membershipID, "admin"); err != nil {
			return err
		}
		return s.appendMember(ctx, accountID, actorID, targetID, "admin")
	})
	s.Require().NoError(err)

	role, _ := s.storedMembership(accountID, targetID)
	s.Equal("admin", role)
	rows, total, err := s.activity.List(ctx, accountID, 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(1), total)
	s.Require().Len(rows, 1)
	s.Equal(activitylog.ActionMemberRoleChanged, rows[0].Action)
	s.Equal("admin", rows[0].Metadata["role"])
	s.NotContains(rows[0].Metadata, "token")
	s.NotContains(rows[0].Metadata, "secret")
}

func (s *AccountActivityRepositoryTestSuite) TestSettingsWriteAndActivityRollBackTogether() {
	ctx := context.Background()
	accountID := s.account()
	actorID := s.user()

	err := s.activity.Within(ctx, func(ctx context.Context) error {
		if err := s.settings.UpsertMany(ctx, accountID, "account_webhooks", map[string]string{
			"signing_secret": "sealed-ciphertext",
		}); err != nil {
			return err
		}
		meta, err := activitylog.SettingsChange("account_webhooks", []string{"signing_secret"})
		if err != nil {
			return err
		}
		id := accountID
		if err := s.activity.Append(ctx, models.AccountActivity{
			AccountID:   &id,
			ActorUserID: actorID,
			Action:      activitylog.ActionSettingsUpdated,
			TargetType:  activitylog.TargetSettings,
			TargetID:    "account_webhooks",
			Metadata:    meta,
		}); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	s.Require().Error(err)

	stored, err := s.settings.ListGroup(ctx, accountID, "account_webhooks")
	s.Require().NoError(err)
	s.Empty(stored)
	_, total, err := s.activity.List(ctx, accountID, 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(0), total)
}

func (s *AccountActivityRepositoryTestSuite) TestPlatformRowUsesANullAccountID() {
	ctx := context.Background()
	actorID := s.user()
	meta, err := activitylog.FeatureChange("sweep-enabled", false)
	s.Require().NoError(err)
	s.Require().NoError(s.activity.Append(ctx, models.AccountActivity{
		ActorUserID: actorID,
		Action:      activitylog.ActionFeaturesUpdated,
		TargetType:  activitylog.TargetFeature,
		TargetID:    "sweep-enabled",
		Metadata:    meta,
	}))

	var accountID *string
	err = facades.Orm().Query().Raw(
		`SELECT account_id::text FROM account_activity WHERE actor_user_id = ?`,
		actorID,
	).Scan(&accountID)
	s.Require().NoError(err)
	s.Nil(accountID)
}

func (s *AccountActivityRepositoryTestSuite) appendMember(ctx context.Context, accountID, actorID, targetID uuid.UUID, role string) error {
	_, meta, err := activitylog.MemberChange(&role, nil)
	if err != nil {
		return err
	}
	id := accountID
	return s.activity.Append(ctx, models.AccountActivity{
		AccountID:   &id,
		ActorUserID: actorID,
		Action:      activitylog.ActionMemberRoleChanged,
		TargetType:  activitylog.TargetAccountUser,
		TargetID:    targetID.String(),
		Metadata:    meta,
	})
}

func (s *AccountActivityRepositoryTestSuite) account() uuid.UUID {
	s.T().Helper()
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "Activity " + accountID.String()[:8], Status: "active", Environment: "prod",
	}))
	return accountID
}

func (s *AccountActivityRepositoryTestSuite) user() uuid.UUID {
	s.T().Helper()
	userID := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, userID.String()+"@example.com", "hash", "active",
	)
	s.Require().NoError(err)
	return userID
}

func (s *AccountActivityRepositoryTestSuite) membership(accountID, userID uuid.UUID, role string) uuid.UUID {
	s.T().Helper()
	membershipID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: membershipID, AccountID: accountID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	}))
	return membershipID
}

func (s *AccountActivityRepositoryTestSuite) storedMembership(accountID, userID uuid.UUID) (string, string) {
	s.T().Helper()
	var member models.AccountUser
	s.Require().NoError(facades.Orm().Query().
		Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", accountID, userID).
		First(&member))
	return member.Role, member.Status
}
