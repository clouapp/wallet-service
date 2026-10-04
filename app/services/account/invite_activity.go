package account

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
)

func (s *Service) recordMemberInvited(ctx context.Context, accountID, actorID uuid.UUID, invite *models.AccountInvite) error {
	if invite == nil || invite.ID == uuid.Nil || actorID == uuid.Nil || accountID == uuid.Nil {
		return fmt.Errorf("account invite activity: invite, actor and account are required")
	}
	meta, err := activitylog.MemberInvited(invite.Role)
	if err != nil {
		return err
	}
	account := accountID
	return s.activity.Append(ctx, models.AccountActivity{
		AccountID:   &account,
		ActorUserID: actorID,
		Action:      activitylog.ActionMemberInvited,
		TargetType:  activitylog.TargetAccountInvite,
		TargetID:    invite.ID.String(),
		Metadata:    meta,
	})
}

func (s *Service) recordInviteAccepted(ctx context.Context, accountID, actorID uuid.UUID, invite *models.AccountInvite) error {
	if invite == nil || invite.ID == uuid.Nil || actorID == uuid.Nil || accountID == uuid.Nil {
		return fmt.Errorf("account invite activity: invite, actor and account are required")
	}
	meta, err := activitylog.InviteAccepted(invite.Role)
	if err != nil {
		return err
	}
	account := accountID
	return s.activity.Append(ctx, models.AccountActivity{
		AccountID:   &account,
		ActorUserID: actorID,
		Action:      activitylog.ActionInviteAccepted,
		TargetType:  activitylog.TargetAccountInvite,
		TargetID:    invite.ID.String(),
		Metadata:    meta,
	})
}
