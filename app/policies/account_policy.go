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
//	account.add-user, account.remove-user, account.freeze, account.archive, account.manage-tokens
type AccountPolicy struct{}

// userRole fetches the caller's role in the given account.
func userRole(ctx context.Context, accountID uuid.UUID) string {
	userID, ok := ctx.Value("user_id").(uuid.UUID)
	if !ok {
		return ""
	}
	au, err := container.Get().AccountUserRepo.FindByAccountAndUser(ctx, accountID, userID)
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

func (p *AccountPolicy) ManageTokens(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	accountID, ok := arguments["account_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing account_id")
	}
	role := userRole(ctx, accountID)
	if role == "owner" || role == "admin" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only owners and admins may manage tokens")
}

// AccountUpdate is the account.update decision for one account.
func AccountUpdate(ctx context.Context, accountID uuid.UUID) contractsaccess.Response {
	return (&AccountPolicy{}).Update(ctx, map[string]any{"account_id": accountID})
}

// AccountArchive is the account.archive decision for one account.
func AccountArchive(ctx context.Context, accountID uuid.UUID) contractsaccess.Response {
	return (&AccountPolicy{}).Archive(ctx, map[string]any{"account_id": accountID})
}

// AccountFreeze is the account.freeze decision for one account.
func AccountFreeze(ctx context.Context, accountID uuid.UUID) contractsaccess.Response {
	return (&AccountPolicy{}).Freeze(ctx, map[string]any{"account_id": accountID})
}

// AccountAddUser is the account.add-user decision for one account.
func AccountAddUser(ctx context.Context, accountID uuid.UUID) contractsaccess.Response {
	return (&AccountPolicy{}).AddUser(ctx, map[string]any{"account_id": accountID})
}

// AccountRemoveUser is the account.remove-user decision for one account.
func AccountRemoveUser(ctx context.Context, accountID uuid.UUID) contractsaccess.Response {
	return (&AccountPolicy{}).RemoveUser(ctx, map[string]any{"account_id": accountID})
}

// AccountManageTokens is the account.manage-tokens decision for one account.
func AccountManageTokens(ctx context.Context, accountID uuid.UUID) contractsaccess.Response {
	return (&AccountPolicy{}).ManageTokens(ctx, map[string]any{"account_id": accountID})
}
