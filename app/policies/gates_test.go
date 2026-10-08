package policies

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/auth/access"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"

	"github.com/macrowallets/waas/app/models"
)

var (
	gateTestRoles = []string{
		models.AccountRoleOwner, models.AccountRoleAdmin, models.AccountRoleAuditor, models.AccountRoleUser,
		models.RetiredAccountRoleViewer, "", "superuser",
	}
	gateTestWalletRoles = []string{
		"", models.WalletRoleViewer, models.WalletRoleSpender, models.WalletRoleApprover, models.WalletRoleAdmin, roleOwner,
	}
)

func definedGate() contractsaccess.Gate {
	gate := access.NewGate(context.Background())
	DefineGates(gate)
	return gate
}

// TestGate_Abilities_AnswerLikeThePolicyTheyWrap asks every ability through
// the Gate and compares the answer, refusal sentence included, with the
// policy function the route guard called before it moved onto the Gate.
func TestGate_Abilities_AnswerLikeThePolicyTheyWrap(t *testing.T) {
	gate := definedGate()

	for _, permission := range models.AccountPermissions() {
		for _, role := range gateTestRoles {
			for _, grants := range []Grants{AccountRoleGrants(role), WalletGrants(role), nil} {
				got := gate.Inspect(permission, map[string]any{ArgGrants: grants})
				assertGateAnswer(t, permission+" as "+role, got, Can(grants, permission), MsgForbidden)
			}
		}
	}

	for _, role := range gateTestRoles {
		grants := AccountRoleGrants(role)
		assertGateAnswer(t, "update member as "+role, gate.Inspect(AbilityAccountUpdateMember, map[string]any{ArgGrants: grants}),
			Can(grants, PermUsersWrite), MsgManageMembers)

		withRole := map[string]any{ArgAccountRole: role}
		assertGateAnswer(t, "view settings as "+role, gate.Inspect(AbilityAccountViewSettings, withRole),
			MayViewSettings(role), MsgSettingsViewDenied)
		assertGateAnswer(t, "update settings as "+role, gate.Inspect(AbilityAccountUpdateSettings, withRole),
			MayUpdateSettings(role), MsgSettingsUpdateDenied)
		assertGateAnswer(t, "read activity as "+role, gate.Inspect(AbilityAccountReadActivity, withRole),
			MayReadActivity(role), MsgActivityReadDenied)
		assertGateAnswer(t, "view features as "+role, gate.Inspect(AbilityAccountViewFeatures, withRole),
			MayViewSettings(role), MsgFeaturesViewDenied)
	}

	walletDecisions := map[string]func(WalletMembership) contractsaccess.Response{
		AbilityWalletFreeze:         WalletFreeze,
		AbilityWalletArchive:        WalletArchive,
		AbilityWalletAddUser:        WalletAddUser,
		AbilityWalletRemoveUser:     WalletRemoveUser,
		AbilityWalletWhitelist:      WalletWhitelist,
		AbilityWalletManageWebhooks: WalletManageWebhooks,
	}
	caller, other := uuid.New(), uuid.New()
	for _, accountRole := range gateTestRoles {
		for _, walletRole := range gateTestWalletRoles {
			for _, userID := range []uuid.UUID{caller, uuid.Nil} {
				membership := WalletMembership{WalletRole: walletRole, AccountRole: accountRole, UserID: userID}
				arguments := map[string]any{ArgWalletRole: walletRole, ArgAccountRole: accountRole, ArgUserID: userID}
				name := "account " + accountRole + " wallet " + walletRole
				for ability, decide := range walletDecisions {
					want := decide(membership)
					assertGateAnswer(t, ability+" "+name, gate.Inspect(ability, arguments), want.Allowed(), want.Message())
				}
				for _, creator := range []uuid.UUID{caller, other, uuid.Nil} {
					withCreator := map[string]any{ArgCreatorID: creator}
					for key, value := range arguments {
						withCreator[key] = value
					}
					want := WalletCancelWithdrawal(membership, creator)
					assertGateAnswer(t, "cancel "+name, gate.Inspect(AbilityWalletCancelWithdrawal, withCreator),
						want.Allowed(), want.Message())
				}
			}
		}
	}
}

// TestGate_Abilities_RefuseMissingOrMistypedArguments fails closed: an
// ability never admits on an argument it cannot read.
func TestGate_Abilities_RefuseMissingOrMistypedArguments(t *testing.T) {
	gate := definedGate()
	for _, name := range Abilities() {
		for _, arguments := range []map[string]any{
			nil,
			{},
			{ArgGrants: map[string]struct{}{name: {}}},
			{ArgGrants: []string{name}, ArgAccountRole: 3, ArgWalletRole: true, ArgUserID: "caller", ArgCreatorID: "caller"},
		} {
			if gate.Inspect(name, arguments).Allowed() {
				t.Errorf("%s admitted %#v", name, arguments)
			}
		}
	}
}

// TestGate_Defines_EveryAbilityItLists keeps the catalog closed: what
// IsAbility accepts is what DefineGates registers, every account permission
// is one of them, and an unknown name is the Gate's own refusal, which a
// route guard must never put in a body.
func TestGate_Defines_EveryAbilityItLists(t *testing.T) {
	gate := definedGate()
	for _, name := range Abilities() {
		if !IsAbility(name) {
			t.Errorf("Abilities lists %q but IsAbility refuses it", name)
		}
		if message := gate.Inspect(name, nil).Message(); strings.HasPrefix(message, "ability doesn't exist") {
			t.Errorf("%q is listed but not defined: %s", name, message)
		}
	}
	for _, permission := range models.AccountPermissions() {
		if !IsAbility(permission) {
			t.Errorf("account permission %q has no ability", permission)
		}
	}
	if IsAbility("nope") {
		t.Fatal("IsAbility accepted an unknown name")
	}
	if message := gate.Inspect("nope", nil).Message(); message != "ability doesn't exist: nope" {
		t.Fatalf("unknown ability answered %q", message)
	}
}

func assertGateAnswer(t *testing.T, name string, got contractsaccess.Response, allowed bool, refusal string) {
	t.Helper()
	if got.Allowed() != allowed {
		t.Errorf("%s: allowed = %t, want %t", name, got.Allowed(), allowed)
		return
	}
	if !allowed && got.Message() != refusal {
		t.Errorf("%s: refusal %q, want %q", name, got.Message(), refusal)
	}
}
