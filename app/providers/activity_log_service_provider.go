package providers

import (
	"fmt"

	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/packages/activitylog"
)

const activityLogName = "audit"

// ActivityLogServiceProvider registers the tables the slotkit capture package
// watches and installs the gorm plugin. Tables the plan names that do not
// exist on this branch are not registered: account_invites, account_role_permissions,
// model_has_roles and role_has_permissions. users.suspended_at is not a column
// here, so it is not on the users allowlist.
type ActivityLogServiceProvider struct{}

func (p *ActivityLogServiceProvider) Register(foundation.Application) {
	for _, table := range auditedTables() {
		if err := activitylog.Register(table); err != nil {
			panic(fmt.Errorf("activitylog: %w", err))
		}
	}
}

func (p *ActivityLogServiceProvider) Boot(app foundation.Application) {
	if err := activitylog.Install(app.MakeOrm()); err != nil {
		panic(fmt.Errorf("activitylog: %w", err))
	}
}

func auditedTables() []activitylog.Table {
	return []activitylog.Table{
		{
			Name:    "accounts",
			LogName: activityLogName,
			Subject: "account",
			Columns: []string{"name", "status", "environment", "view_all_wallets"},
		},
		{
			Name:    "account_users",
			LogName: activityLogName,
			Subject: "account_user",
			Columns: []string{"account_id", "user_id", "role", "status"},
		},
		{
			Name:    "wallet_users",
			LogName: activityLogName,
			Subject: "wallet_user",
			Columns: []string{"wallet_id", "user_id", "roles", "status"},
		},
		{
			Name:    "wallets",
			LogName: activityLogName,
			Subject: "wallet",
			Columns: []string{"label", "status", "fee_rate_min", "fee_rate_max", "fee_multiplier", "required_approvals"},
		},
		{
			Name:    "whitelist_entries",
			LogName: activityLogName,
			Subject: "whitelist_entry",
			Columns: []string{"wallet_id", "label", "address"},
		},
		{
			Name:    "webhook_configs",
			LogName: activityLogName,
			Subject: "webhook_config",
			Columns: []string{"url", "events", "is_active", "wallet_id", "type"},
		},
		{
			Name:    "access_tokens",
			LogName: activityLogName,
			Subject: "access_token",
			Columns: []string{"account_id", "name", "permissions", "ip_cidr"},
		},
		{
			Name:       "settings",
			LogName:    activityLogName,
			Subject:    "setting",
			Columns:    []string{"account_id", "group", "key"},
			KeyColumns: []string{"id"},
		},
		{
			Name:       "platform_admins",
			LogName:    activityLogName,
			Subject:    "platform_admin",
			Columns:    []string{"user_id"},
			KeyColumns: []string{"user_id"},
		},
		{
			Name:    "users",
			LogName: activityLogName,
			Subject: "user",
			Columns: []string{"email", "full_name", "status", "totp_enabled"},
		},
		{
			Name:       "features",
			LogName:    activityLogName,
			Subject:    "feature",
			Columns:    []string{"account_id", "key", "enabled"},
			KeyColumns: []string{"id"},
		},
		{
			Name:       "global_features",
			LogName:    activityLogName,
			Subject:    "feature",
			Columns:    []string{"key", "enabled"},
			KeyColumns: []string{"id"},
		},
		{
			Name:    "chains",
			LogName: activityLogName,
			Subject: "chain",
			Columns: []string{
				"status", "required_confirmations",
				"gas_readiness_threshold_raw", "dust_threshold_native_raw", "dust_threshold_usd",
			},
		},
	}
}
