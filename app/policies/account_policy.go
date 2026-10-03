package policies

import (
	"context"

	"github.com/google/uuid"
	"github.com/goravel/framework/auth/access"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"
)

// AccountPolicy defines gate abilities for Account resources.
// Abilities: account.view, account.update, account.delete,
//
//	account.add-user, account.remove-user, account.freeze, account.archive, account.manage-tokens
type AccountPolicy struct{}

// userRole is the caller's role in the account. The caller passes the user id.
func userRole(ctx context.Context, accountID uuid.UUID, arguments map[string]any) string {
	if arguments == nil {
		return ""
	}
	userID, ok := arguments["user_id"].(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return ""
	}
	au, err := accountUserRepository().FindByAccountAndUser(ctx, accountID, userID)
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
	role := userRole(ctx, accountID, arguments)
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
	role := userRole(ctx, accountID, arguments)
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
	if userRole(ctx, accountID, arguments) == "owner" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only owners may delete accounts")
}

func (p *AccountPolicy) AddUser(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	accountID, ok := arguments["account_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing account_id")
	}
	role := userRole(ctx, accountID, arguments)
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
	role := userRole(ctx, accountID, arguments)
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
	if userRole(ctx, accountID, arguments) == "owner" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only owners may freeze accounts")
}

func (p *AccountPolicy) Archive(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	accountID, ok := arguments["account_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing account_id")
	}
	if userRole(ctx, accountID, arguments) == "owner" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only owners may archive accounts")
}

func (p *AccountPolicy) ManageTokens(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	accountID, ok := arguments["account_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing account_id")
	}
	role := userRole(ctx, accountID, arguments)
	if role == "owner" || role == "admin" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only owners and admins may manage tokens")
}

func accountDecisionArguments(accountID, userID uuid.UUID) map[string]any {
	arguments := map[string]any{"account_id": accountID}
	if userID != uuid.Nil {
		arguments["user_id"] = userID
	}
	return arguments
}

// AccountUpdate is the account.update decision for one account and the caller id.
func AccountUpdate(ctx context.Context, accountID, userID uuid.UUID) contractsaccess.Response {
	return (&AccountPolicy{}).Update(ctx, accountDecisionArguments(accountID, userID))
}

// AccountArchive is the account.archive decision for one account and the caller id.
func AccountArchive(ctx context.Context, accountID, userID uuid.UUID) contractsaccess.Response {
	return (&AccountPolicy{}).Archive(ctx, accountDecisionArguments(accountID, userID))
}

// AccountFreeze is the account.freeze decision for one account and the caller id.
func AccountFreeze(ctx context.Context, accountID, userID uuid.UUID) contractsaccess.Response {
	return (&AccountPolicy{}).Freeze(ctx, accountDecisionArguments(accountID, userID))
}

// AccountAddUser is the account.add-user decision for one account and the caller id.
func AccountAddUser(ctx context.Context, accountID, userID uuid.UUID) contractsaccess.Response {
	return (&AccountPolicy{}).AddUser(ctx, accountDecisionArguments(accountID, userID))
}

// AccountRemoveUser is the account.remove-user decision for one account and the caller id.
func AccountRemoveUser(ctx context.Context, accountID, userID uuid.UUID) contractsaccess.Response {
	return (&AccountPolicy{}).RemoveUser(ctx, accountDecisionArguments(accountID, userID))
}

// AccountManageTokens is the account.manage-tokens decision for one account and the caller id.
func AccountManageTokens(ctx context.Context, accountID, userID uuid.UUID) contractsaccess.Response {
	return (&AccountPolicy{}).ManageTokens(ctx, accountDecisionArguments(accountID, userID))
}
