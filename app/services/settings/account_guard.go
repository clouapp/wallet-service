package settings

import (
	"fmt"

	"github.com/macrowallets/waas/app/policies"
)

// AccountGuard is the catalog entry the account dashboard settings routes
// already use. S1.4.7 names settings.read and settings.write. Owner, admin,
// and auditor hold the read. Owner and admin hold the write. Auditor does
// not write. The user role holds neither. settings.security.write on
// account_security is optional and is not required, so this entry does not
// name it and it is not a gate. Platform names stay settings.view and
// settings.update.
type AccountGuard struct {
	Read  string
	Write string
}

// AccountSettingsCatalog is the code catalog the account settings routes use.
func AccountSettingsCatalog() AccountGuard {
	return AccountGuard{
		Read:  policies.PermSettingsRead,
		Write: policies.PermSettingsWrite,
	}
}

func requireAccountGuard(catalog AccountGuard) error {
	if catalog.Read != policies.PermSettingsRead || catalog.Write != policies.PermSettingsWrite {
		return fmt.Errorf("account settings: settings.read and settings.write are required")
	}
	if catalog.Read == policies.PermSettingsView || catalog.Write == policies.PermSettingsUpdate ||
		catalog.Read == policies.PermSettingsUpdate || catalog.Write == policies.PermSettingsView ||
		catalog.Read == catalog.Write {
		return fmt.Errorf("account settings: the platform pair is not the account guard")
	}
	return nil
}
