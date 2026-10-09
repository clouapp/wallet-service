package walletrecords

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/pgerr"
)

var (
	// ErrNotAccountMember is AddMember's refusal of a user who is not an active
	// member of the wallet's account, and of one who does not exist.
	ErrNotAccountMember = errors.New("user is not an active member of this account")
	// ErrMembershipLookup wraps a failed read of the user's existing wallet
	// membership. The cause stays on the error chain.
	ErrMembershipLookup = errors.New("failed to load membership")
	// ErrAlreadyWalletMember is AddMember's refusal of a user whose membership is
	// on the wallet already; a removed one is restored instead.
	ErrAlreadyWalletMember = errors.New("user is already a member of this wallet")
	// ErrMembershipRestore wraps a failed restore of a removed membership.
	ErrMembershipRestore = errors.New("failed to restore wallet user")
)

const memberStatusActive = "active"

// AddMember gives a user roles on the wallet. The roles are a comma-separated
// set of wallet roles (models.ErrInvalidWalletRoles otherwise) and the user must
// be an active member of the wallet's account (ErrNotAccountMember otherwise).
// A membership that was removed is restored with the new roles; a failure to set
// the roles on it is logged, as the restore already holds.
func (m *Memberships) AddMember(ctx context.Context, wallet *models.Wallet, userID uuid.UUID, roles string) (*models.WalletUser, error) {
	parsed, err := models.ParseWalletRoles(roles)
	if err != nil {
		return nil, err
	}
	roleList := models.FormatWalletRoles(parsed)

	if err := m.requireActiveAccountMember(ctx, wallet, userID); err != nil {
		return nil, err
	}

	existing, err := m.members.FindByWalletAndUserIncludeDeleted(ctx, wallet.ID, userID)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		slog.Error("wallet-users: lookup existing", "wallet", wallet.ID, "error", err)
		return nil, fmt.Errorf("%w: %w", ErrMembershipLookup, err)
	}
	if existing != nil && existing.DeletedAt == nil {
		return nil, ErrAlreadyWalletMember
	}
	if existing != nil {
		if err := m.members.Restore(ctx, existing.ID); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMembershipRestore, err)
		}
		existing.DeletedAt = nil
		if err := m.members.SetRoles(ctx, existing.ID, roleList); err != nil {
			slog.Error("wallet-users: update roles", "wallet", wallet.ID, "error", err)
		} else {
			existing.Roles = roleList
		}
		return existing, nil
	}

	member := &models.WalletUser{
		ID:       uuid.New(),
		WalletID: wallet.ID,
		UserID:   userID,
		Roles:    roleList,
		Status:   memberStatusActive,
	}
	if err := m.members.Create(ctx, member); err != nil {
		if pgerr.IsUniqueViolation(err) {
			return nil, ErrAlreadyWalletMember
		}
		return nil, fmt.Errorf("add wallet user: %w", err)
	}
	return member, nil
}

// requireActiveAccountMember refuses a user who is not an active member of the
// wallet's account. A wallet without an account has no such member.
func (m *Memberships) requireActiveAccountMember(ctx context.Context, wallet *models.Wallet, userID uuid.UUID) error {
	if wallet.AccountID == nil {
		return ErrNotAccountMember
	}
	member, err := m.accounts.FindMember(ctx, *wallet.AccountID, userID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return ErrNotAccountMember
		}
		return fmt.Errorf("find account member: %w", err)
	}
	if member == nil || member.Status != memberStatusActive {
		return ErrNotAccountMember
	}
	return nil
}
