package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

var (
	// ErrUserLookup is a lookup of the email to add that failed for a reason
	// other than a missing user.
	ErrUserLookup = errors.New("look up the user to add")
	// ErrMemberNotAdded is a membership write for an existing user that failed.
	ErrMemberNotAdded = errors.New("add the member")
	// ErrMemberUnreadable is a membership just written that could not be read back.
	ErrMemberUnreadable = errors.New("read back the added member")
)

// AddMemberInput is POST /v1/accounts/{accountId}/users: the caller adds the
// email to the account with a role.
type AddMemberInput struct {
	AccountID uuid.UUID
	ActorID   uuid.UUID
	Email     string
	Role      string
}

// AddedMember is what AddMember did: Member is set when the email had a user,
// who is now a member; Invite is set when no user had it and an invite was
// issued. Neither is set when the membership was written but is not found on
// the read back.
type AddedMember struct {
	Member *models.AccountUser
	Invite *IssuedInvite
}

// AddMember adds the user that owns the email to the account, or invites the
// email when no user owns it. A role above the caller's is ErrGrantRole on both
// paths. The other failures name their step: ErrUserLookup, ErrMemberNotAdded,
// ErrMemberUnreadable, and on the invite path the errors of InviteMember.
func (s *Service) AddMember(ctx context.Context, in AddMemberInput) (AddedMember, error) {
	user, err := s.FindUserByEmail(ctx, in.Email)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return AddedMember{}, fmt.Errorf("%w: %w", ErrUserLookup, err)
	}
	if user == nil || err != nil {
		invite, err := s.InviteMember(ctx, InviteInput{
			AccountID: in.AccountID,
			InvitedBy: in.ActorID,
			Email:     in.Email,
			Role:      in.Role,
		})
		if err != nil {
			return AddedMember{}, err
		}
		return AddedMember{Invite: invite}, nil
	}

	if err := s.AddUser(ctx, in.AccountID, user.ID, in.Role, in.ActorID); err != nil {
		return AddedMember{}, fmt.Errorf("%w: %w", ErrMemberNotAdded, err)
	}
	member, err := s.FindMember(ctx, in.AccountID, user.ID)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return AddedMember{}, fmt.Errorf("%w: %w", ErrMemberUnreadable, err)
	}
	if err != nil {
		return AddedMember{}, nil
	}
	return AddedMember{Member: member}, nil
}

// InviteInput is an invite to issue: the account, the inviter, the email and
// the role it grants.
type InviteInput struct {
	AccountID uuid.UUID
	InvitedBy uuid.UUID
	Email     string
	Role      string
}

// InviteMember is IssueInvite with the link based at APP_FRONTEND_URL. A
// missing APP_FRONTEND_URL is ErrFrontendURLRequired and nothing is stored. An
// invite mail that could not be queued is logged; the invite stands.
func (s *Service) InviteMember(ctx context.Context, in InviteInput) (*IssuedInvite, error) {
	base, err := FrontendBase()
	if err != nil {
		return nil, err
	}
	issued, err := s.IssueInvite(ctx, in.AccountID, in.Email, in.Role, in.InvitedBy, base)
	if err != nil {
		return nil, err
	}
	logInviteMail(issued)
	return issued, nil
}

// ResendInviteLink is ResendInvite with the link based at APP_FRONTEND_URL,
// on the same terms as InviteMember.
func (s *Service) ResendInviteLink(ctx context.Context, accountID, inviteID uuid.UUID) (*IssuedInvite, error) {
	base, err := FrontendBase()
	if err != nil {
		return nil, err
	}
	issued, err := s.ResendInvite(ctx, accountID, inviteID, base)
	if err != nil {
		return nil, err
	}
	logInviteMail(issued)
	return issued, nil
}

// logInviteMail records a dispatch failure without the token, its hash or the link.
func logInviteMail(issued *IssuedInvite) {
	if issued.MailErr != nil {
		slog.Error("account: send invite mail failed")
	}
}
