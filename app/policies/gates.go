package policies

import (
	"context"
	"sort"

	"github.com/google/uuid"
	"github.com/goravel/framework/auth/access"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"

	"github.com/macrowallets/waas/app/models"
)

// ArgGrants is the request's permission set (Grants) for a catalog ability.
const ArgGrants = "grants"

// Account abilities the route guards ask beside the permission catalog. Each
// one is the role rule its service already applies, refused with the
// sentence that service answers.
const (
	AbilityAccountUpdateMember   = "account.update-member"
	AbilityAccountViewSettings   = "account.view-settings"
	AbilityAccountUpdateSettings = "account.update-settings"
	AbilityAccountReadActivity   = "account.read-activity"
	AbilityAccountViewFeatures   = "account.view-features"
)

// Refusal sentences of the abilities. The services build their sentinels
// from these, so a 403 body has one source.
const (
	MsgForbidden            = "forbidden"
	MsgManageMembers        = "only owners and admins may manage members"
	MsgSettingsViewDenied   = "you do not have permission to view account settings"
	MsgSettingsUpdateDenied = "you do not have permission to update account settings"
	MsgActivityReadDenied   = "you do not have permission to view account activity"
	MsgFeaturesViewDenied   = "you do not have permission to view account features"
)

// ability decides from its arguments alone: the caller loads the role,
// grants and membership, the ability only answers. A missing or wrongly
// typed argument is the zero value, which every rule refuses.
type ability func(arguments map[string]any) contractsaccess.Response

// DefineGates registers every permission ability on gate. bootstrap calls it
// from WithCallback; the route guards ask it through the Gate facade.
func DefineGates(gate contractsaccess.Gate) {
	for name, decide := range gateAbilities() {
		gate.Define(name, func(_ context.Context, arguments map[string]any) contractsaccess.Response {
			return decide(arguments)
		})
	}
}

// IsAbility reports whether DefineGates registers name. The Gate answers an
// unknown name with "ability doesn't exist: <name>", which must never reach
// a response body, so a guard refuses to be built for one.
func IsAbility(name string) bool {
	_, ok := gateAbilities()[name]
	return ok
}

// Abilities lists the names DefineGates registers, sorted.
func Abilities() []string {
	abilities := gateAbilities()
	names := make([]string, 0, len(abilities))
	for name := range abilities {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func gateAbilities() map[string]ability {
	abilities := map[string]ability{
		AbilityAccountUpdateMember:    grantAbility(PermUsersWrite, MsgManageMembers),
		AbilityAccountViewSettings:    roleAbility(MayViewSettings, MsgSettingsViewDenied),
		AbilityAccountUpdateSettings:  roleAbility(MayUpdateSettings, MsgSettingsUpdateDenied),
		AbilityAccountReadActivity:    roleAbility(MayReadActivity, MsgActivityReadDenied),
		AbilityAccountViewFeatures:    roleAbility(MayViewSettings, MsgFeaturesViewDenied),
		AbilityWalletFreeze:           walletAbility(WalletFreeze),
		AbilityWalletArchive:          walletAbility(WalletArchive),
		AbilityWalletUpdate:           walletAbility(WalletUpdate),
		AbilityWalletAddUser:          walletAbility(WalletAddUser),
		AbilityWalletRemoveUser:       walletAbility(WalletRemoveUser),
		AbilityWalletWhitelist:        walletAbility(WalletWhitelist),
		AbilityWalletManageWebhooks:   walletAbility(WalletManageWebhooks),
		AbilityWalletCancelWithdrawal: cancelWithdrawalAbility,
	}
	for _, permission := range models.AccountPermissions() {
		abilities[permission] = grantAbility(permission, MsgForbidden)
	}
	return abilities
}

// grantAbility allows when ArgGrants holds permission.
func grantAbility(permission, refusal string) ability {
	return func(arguments map[string]any) contractsaccess.Response {
		grants, _ := arguments[ArgGrants].(Grants)
		if Can(grants, permission) {
			return access.NewAllowResponse()
		}
		return access.NewDenyResponse(refusal)
	}
}

// roleAbility allows when rule holds for ArgAccountRole.
func roleAbility(rule func(role string) bool, refusal string) ability {
	return func(arguments map[string]any) contractsaccess.Response {
		role, _ := arguments[ArgAccountRole].(string)
		if rule(role) {
			return access.NewAllowResponse()
		}
		return access.NewDenyResponse(refusal)
	}
}

// walletAbility asks decide with the membership in ArgWalletRole,
// ArgAccountRole and ArgUserID.
func walletAbility(decide func(WalletMembership) contractsaccess.Response) ability {
	return func(arguments map[string]any) contractsaccess.Response {
		return decide(membershipFrom(arguments))
	}
}

// cancelWithdrawalAbility also reads the creator in ArgCreatorID.
func cancelWithdrawalAbility(arguments map[string]any) contractsaccess.Response {
	creatorID, _ := arguments[ArgCreatorID].(uuid.UUID)
	return WalletCancelWithdrawal(membershipFrom(arguments), creatorID)
}
