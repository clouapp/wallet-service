package policies

import (
	"context"

	"github.com/google/uuid"
	"github.com/goravel/framework/auth/access"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"

	"github.com/macrowallets/waas/app/container"
)

// AccountPolicy defines gate abilities for Account resources.
// Abilities: account.view, account.update, account.delete,
//
//	account.add-user, account.remove-user, account.freeze, account.archive
type AccountPolicy struct{}

// userRole fetches the caller's role in the given account.
func userRole(ctx context.Context, accountID uuid.UUID) string {
	userID, ok := ctx.Value("user_id").(uuid.UUID)
	if !ok {
		return ""
	}
	au, err := container.Get().AccountUserRepo.FindByAccountAndUser(accountID, userID)
	if err != nil || au == nil {
		return ""
	}
	return au.Role
}

func (p *AccountPolicy) View(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	accountID, ok := arguments["account_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing account_id")
	}
	role := userRole(ctx, accountID)
	if role == "" {
		return access.NewDenyResponse("not a member of this account")
	}
	return access.NewAllowResponse()
}

func (p *AccountPolicy) Update(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	accountID, ok := arguments["account_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing account_id")
	}
	role := userRole(ctx, accountID)
	if role == "owner" || role == "admin" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only owners and admins may update account settings")
}

func (p *AccountPolicy) Delete(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	accountID, ok := arguments["account_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing account_id")
	}
	if userRole(ctx, accountID) == "owner" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only owners may delete accounts")
}

func (p *AccountPolicy) AddUser(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	accountID, ok := arguments["account_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing account_id")
	}
	role := userRole(ctx, accountID)
	if role == "owner" || role == "admin" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only owners and admins may add users")
}

func (p *AccountPolicy) RemoveUser(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	accountID, ok := arguments["account_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing account_id")
	}
	role := userRole(ctx, accountID)
	if role == "owner" || role == "admin" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only owners and admins may remove users")
}

func (p *AccountPolicy) Freeze(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	accountID, ok := arguments["account_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing account_id")
	}
	if userRole(ctx, accountID) == "owner" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only owners may freeze accounts")
}

func (p *AccountPolicy) Archive(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	accountID, ok := arguments["account_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing account_id")
	}
	if userRole(ctx, accountID) == "owner" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only owners may archive accounts")
}
