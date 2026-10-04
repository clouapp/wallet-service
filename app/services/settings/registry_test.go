package settings

import (
	"testing"

	"github.com/macrowallets/waas/app/policies"
)

func TestAccountSectionsDoNotShareNamesWithGroups(t *testing.T) {
	t.Parallel()

	names := map[string]bool{}
	var sawSecret bool
	var sawDecimal bool
	for _, group := range Registry() {
		if names[group.Name] {
			t.Fatalf("duplicate group %s", group.Name)
		}
		names[group.Name] = true
		if group.SectionName() == group.Name {
			t.Fatalf("section %s shares its name with the group", group.Name)
		}
		switch group.Scope {
		case ScopeAccount:
		case ScopePlatform:
			if !knownPlatformGroup(group.Name) {
				t.Fatalf("unexpected platform group %s", group.Name)
			}
		default:
			t.Fatalf("group %s has unknown scope %s", group.Name, group.Scope)
		}
		for _, definition := range group.Settings {
			if definition.Secret {
				sawSecret = true
			}
			if definition.Type == TypeDecimal {
				sawDecimal = true
			}
		}
	}
	if !sawSecret {
		t.Fatal("registry has no secret")
	}
	if !sawDecimal {
		t.Fatal("registry has no decimal")
	}
	limits, ok := FindGroup(groupAccountSweepLimits)
	if !ok || limits.ManagedBy != ManagedByPlatform || limits.Inherits != groupSweepLimits || !sweepLimitPermissions(limits) {
		t.Fatalf("sweep limits group = %+v present %v", limits, ok)
	}
	platformLimits, ok := FindGroup(groupSweepLimits)
	if !ok || platformLimits.Scope != ScopePlatform || !sweepLimitPermissions(platformLimits) ||
		platformLimits.SectionName() != sectionSweep || len(platformLimits.Settings) != len(limits.Settings) {
		t.Fatalf("platform sweep limits = %+v present %v", platformLimits, ok)
	}
	if limits.UpdatePermission == policies.PermSettingsUpdate || platformLimits.UpdatePermission == policies.PermSettingsUpdate {
		t.Fatal("holding settings.update does not by itself become sweep.update")
	}
	for i, definition := range limits.Settings {
		if platformLimits.Settings[i].Key != definition.Key {
			t.Fatalf("platform key %s, account key %s", platformLimits.Settings[i].Key, definition.Key)
		}
	}
	scan, ok := FindGroup(groupDepositScan)
	if !ok || scan.Scope != ScopePlatform || len(scan.Settings) != 3 {
		t.Fatalf("deposit scan group = %+v present %v", scan, ok)
	}
	delivery, ok := FindGroup(groupWebhookDelivery)
	if !ok || delivery.Scope != ScopePlatform || len(delivery.Settings) != 2 || delivery.UpdatePermission != "" {
		t.Fatalf("webhook delivery group = %+v present %v", delivery, ok)
	}
	smtp, ok := FindGroup(groupMailSMTP)
	if !ok || smtp.Scope != ScopePlatform || smtp.SectionName() != sectionMail || !mailCredentialPermissions(smtp) || len(smtp.Settings) != 5 {
		t.Fatalf("mail smtp group = %+v present %v", smtp, ok)
	}
	host, ok := Find(groupMailSMTP, keyMailHost)
	if !ok || !host.Destination || host.Secret {
		t.Fatalf("mail host = %+v present %v", host, ok)
	}
	password, ok := Find(groupMailSMTP, keyMailPassword)
	if !ok || !password.Secret || password.Type != TypeString || password.Destination {
		t.Fatalf("mail password = %+v present %v", password, ok)
	}
	mailFrom, ok := FindGroup(groupMailDelivery)
	if !ok || mailFrom.Scope != ScopePlatform || mailFrom.SectionName() != sectionMail || mailFrom.ViewPermission != "" || mailFrom.UpdatePermission != "" || len(mailFrom.Settings) != 3 {
		t.Fatalf("mail delivery group = %+v present %v", mailFrom, ok)
	}
	fromAddress, ok := Find(groupMailDelivery, keyMailFromAddress)
	if !ok || fromAddress.Secret || fromAddress.Type != TypeString {
		t.Fatalf("from address = %+v present %v", fromAddress, ok)
	}
	fromName, ok := Find(groupMailDelivery, keyMailFromName)
	if !ok || fromName.Secret || fromName.Type != TypeString {
		t.Fatalf("from name = %+v present %v", fromName, ok)
	}
	driver, ok := Find(groupMailDelivery, keyMailDriver)
	if !ok || driver.Secret || len(driver.Options) == 0 {
		t.Fatalf("mail driver = %+v present %v", driver, ok)
	}
	assertMailProviderGroups(t)
	assertPriceGroups(t)
	assertWebhookProviderGroups(t)
	assertEtherscanProviderGroup(t)
}

func knownPlatformGroup(name string) bool {
	switch name {
	case groupDepositScan, groupWebhookDelivery, groupSweepLimits, groupMailSMTP, groupMailDelivery,
		groupMailSES, groupMailMailgun, groupMailResend, groupMailPostmark,
		groupPriceLookup, groupPriceCoinGecko, groupPriceCoinMarketCap, groupPriceCoinAPI,
		groupProviderAlchemy, groupProviderHelius, groupProviderQuickNode, groupProviderEtherscan:
		return true
	default:
		return false
	}
}

func assertMailProviderGroups(t *testing.T) {
	t.Helper()
	ses, ok := FindGroup(groupMailSES)
	if !ok || ses.Scope != ScopePlatform || ses.SectionName() != sectionMail || !mailCredentialPermissions(ses) || len(ses.Settings) != 5 {
		t.Fatalf("mail ses group = %+v present %v", ses, ok)
	}
	if len(ses.CredentialGroups) != 1 || len(ses.CredentialGroups[0]) != 2 ||
		ses.CredentialGroups[0][0] != keyMailProviderKey || ses.CredentialGroups[0][1] != keyMailProviderSecret {
		t.Fatalf("ses credential groups = %#v", ses.CredentialGroups)
	}
	sesKey, ok := Find(groupMailSES, keyMailProviderKey)
	if !ok || !sesKey.Secret || sesKey.Destination {
		t.Fatalf("ses key = %+v present %v", sesKey, ok)
	}
	sesSecret, ok := Find(groupMailSES, keyMailProviderSecret)
	if !ok || !sesSecret.Secret {
		t.Fatalf("ses secret = %+v present %v", sesSecret, ok)
	}
	region, ok := Find(groupMailSES, keyMailRegion)
	if !ok || region.Secret {
		t.Fatalf("ses region = %+v present %v", region, ok)
	}
	mailgun, ok := FindGroup(groupMailMailgun)
	if !ok || !mailCredentialPermissions(mailgun) || len(mailgun.Settings) != 5 || len(mailgun.CredentialGroups) != 0 {
		t.Fatalf("mailgun group = %+v present %v", mailgun, ok)
	}
	domain, ok := Find(groupMailMailgun, keyMailDomain)
	if !ok || domain.Secret {
		t.Fatalf("mailgun domain = %+v present %v", domain, ok)
	}
	endpoint, ok := Find(groupMailMailgun, keyMailEndpoint)
	if !ok || endpoint.Secret {
		t.Fatalf("mailgun endpoint = %+v present %v", endpoint, ok)
	}
	mailgunSecret, ok := Find(groupMailMailgun, keyMailProviderSecret)
	if !ok || !mailgunSecret.Secret {
		t.Fatalf("mailgun secret = %+v present %v", mailgunSecret, ok)
	}
	resend, ok := FindGroup(groupMailResend)
	if !ok || !mailCredentialPermissions(resend) || len(resend.Settings) != 3 {
		t.Fatalf("resend group = %+v present %v", resend, ok)
	}
	apiKey, ok := Find(groupMailResend, keyMailAPIKey)
	if !ok || !apiKey.Secret {
		t.Fatalf("resend api key = %+v present %v", apiKey, ok)
	}
	postmark, ok := FindGroup(groupMailPostmark)
	if !ok || !mailCredentialPermissions(postmark) || len(postmark.Settings) != 4 {
		t.Fatalf("postmark group = %+v present %v", postmark, ok)
	}
	token, ok := Find(groupMailPostmark, keyMailToken)
	if !ok || !token.Secret {
		t.Fatalf("postmark token = %+v present %v", token, ok)
	}
	stream, ok := Find(groupMailPostmark, keyMailMessageStream)
	if !ok || stream.Secret {
		t.Fatalf("postmark stream = %+v present %v", stream, ok)
	}
}

func assertPriceGroups(t *testing.T) {
	t.Helper()
	lookup, ok := FindGroup(groupPriceLookup)
	if !ok || lookup.Scope != ScopePlatform || lookup.SectionName() != sectionPrice ||
		lookup.UpdatePermission != "" || lookup.ViewPermission != "" || len(lookup.Settings) != 1 {
		t.Fatalf("price lookup group = %+v present %v", lookup, ok)
	}
	order, ok := Find(groupPriceLookup, keyProviderOrder)
	if !ok || order.Secret || order.Type != TypeStringList || len(order.Options) != len(priceProviderNames()) {
		t.Fatalf("provider order = %+v present %v", order, ok)
	}
	for i, name := range priceProviderNames() {
		if order.Options[i] != name {
			t.Fatalf("provider option %d = %q", i, order.Options[i])
		}
	}
	if _, ok := Find(groupPriceLookup, "base_url"); ok {
		t.Fatal("price lookup declares a base url")
	}
	for _, name := range priceProviderGroupNames() {
		group, found := FindGroup(name)
		if !found || group.Scope != ScopePlatform || group.SectionName() != sectionPrice ||
			!providerCredentialPermissions(group) || len(group.Settings) != 2 ||
			len(group.CredentialGroups) != 0 {
			t.Fatalf("price group %s = %+v present %v", name, group, found)
		}
		enabled, enabledOK := Find(name, keyPriceEnabled)
		if !enabledOK || enabled.Secret || enabled.Type != TypeBool {
			t.Fatalf("%s enabled = %+v present %v", name, enabled, enabledOK)
		}
		apiKey, keyOK := Find(name, keyPriceAPIKey)
		if !keyOK || !apiKey.Secret || apiKey.Type != TypeString || apiKey.Destination {
			t.Fatalf("%s api key = %+v present %v", name, apiKey, keyOK)
		}
		if _, baseOK := Find(name, "base_url"); baseOK {
			t.Fatalf("%s declares a base url", name)
		}
	}
}

func assertWebhookProviderGroups(t *testing.T) {
	t.Helper()
	for _, name := range webhookProviderGroupNames() {
		group, found := FindGroup(name)
		if !found || group.Scope != ScopePlatform || group.SectionName() != sectionProviders ||
			!providerCredentialPermissions(group) || len(group.Settings) != 2 ||
			len(group.CredentialGroups) != 0 {
			t.Fatalf("webhook provider group %s = %+v present %v", name, group, found)
		}
		enabled, enabledOK := Find(name, keyProviderEnabled)
		if !enabledOK || enabled.Secret || enabled.Type != TypeBool {
			t.Fatalf("%s enabled = %+v present %v", name, enabled, enabledOK)
		}
	}
	token, tokenOK := Find(groupProviderAlchemy, keyProviderAuthToken)
	if !tokenOK || !token.Secret || token.Type != TypeString || token.Destination {
		t.Fatalf("alchemy auth token = %+v present %v", token, tokenOK)
	}
	if _, apiOK := Find(groupProviderAlchemy, keyProviderAPIKey); apiOK {
		t.Fatal("alchemy declares an api key")
	}
	for _, name := range []string{groupProviderHelius, groupProviderQuickNode, groupProviderEtherscan} {
		apiKey, keyOK := Find(name, keyProviderAPIKey)
		if !keyOK || !apiKey.Secret || apiKey.Type != TypeString || apiKey.Destination {
			t.Fatalf("%s api key = %+v present %v", name, apiKey, keyOK)
		}
		if _, tokenOK := Find(name, keyProviderAuthToken); tokenOK {
			t.Fatalf("%s declares an auth token", name)
		}
	}
}

func assertEtherscanProviderGroup(t *testing.T) {
	t.Helper()
	group, found := FindGroup(groupProviderEtherscan)
	if !found || group.Scope != ScopePlatform || group.SectionName() != sectionProviders ||
		group.Block != blockHeightProvider || !providerCredentialPermissions(group) ||
		len(group.Settings) != 2 || len(group.CredentialGroups) != 0 {
		t.Fatalf("etherscan group = %+v present %v", group, found)
	}
	enabled, enabledOK := Find(groupProviderEtherscan, keyProviderEnabled)
	if !enabledOK || enabled.Secret || enabled.Type != TypeBool {
		t.Fatalf("etherscan enabled = %+v present %v", enabled, enabledOK)
	}
}

func TestFindGroupUnknown(t *testing.T) {
	t.Parallel()

	if _, ok := FindGroup("nope"); ok {
		t.Fatal("an unknown group was found")
	}
	if _, ok := Find("nope", "port"); ok {
		t.Fatal("an unknown group returned a definition")
	}
}

func TestSectionNameDefaultsToTheGroupName(t *testing.T) {
	t.Parallel()

	group := Group{Name: "auth"}
	if got := group.SectionName(); got != "auth" {
		t.Fatalf("section = %q", got)
	}
}

func TestRegistryEveryGroupReportsASection(t *testing.T) {
	t.Parallel()

	for _, group := range Registry() {
		section := group.SectionName()
		if section == "" {
			t.Fatalf("group %s reports no section", group.Name)
		}
		if section == group.Name {
			continue
		}
		if _, taken := FindGroup(section); taken {
			t.Fatalf("section %s of group %s is also a group name", section, group.Name)
		}
	}
}

func TestRegistryEverySecretDeclaresAType(t *testing.T) {
	t.Parallel()

	for _, group := range Registry() {
		for _, definition := range group.Settings {
			if definition.Secret && definition.Type == "" {
				t.Fatalf("%s.%s is a secret with no type", group.Name, definition.Key)
			}
		}
	}
}

func TestEveryRegistryDefaultCastsThroughItsType(t *testing.T) {
	t.Parallel()

	for _, group := range Registry() {
		for _, definition := range group.Settings {
			if definition.Default == nil {
				t.Fatalf("%s.%s has no default", group.Name, definition.Key)
			}
			stored, err := castIn(definition.Default(), definition)
			if err != nil {
				t.Fatalf("%s.%s default: %v", group.Name, definition.Key, err)
			}
			if stored != defaultStored(definition) {
				t.Fatalf("%s.%s cast %q, defaultStored %q", group.Name, definition.Key, stored, defaultStored(definition))
			}
			if definition.Type == "" {
				t.Fatalf("%s.%s has no type", group.Name, definition.Key)
			}
		}
	}
}

func TestEverySecretGroupDeclaresAPermission(t *testing.T) {
	t.Parallel()

	var sawSecret bool
	for _, group := range Registry() {
		secret := false
		for _, definition := range group.Settings {
			if definition.Secret {
				secret = true
				sawSecret = true
			}
		}
		if !secret {
			continue
		}
		if group.Scope == ScopePlatform {
			if !platformSecretGroupGatedByAdmins(group) {
				t.Fatalf("platform secret group %s must stay gated by platform_admins", group.Name)
			}
			continue
		}
		if group.ViewPermission == "" || group.UpdatePermission == "" {
			t.Fatalf("secret group %s declares no permission", group.Name)
		}
	}
	if !sawSecret {
		t.Fatal("registry has no secret")
	}
	webhooks, ok := FindGroup(groupAccountWebhooks)
	if !ok || webhooks.ViewPermission != permAccountWebhooksView || webhooks.UpdatePermission != permAccountWebhooksUpdate {
		t.Fatalf("webhooks permissions = %q %q present %v", webhooks.ViewPermission, webhooks.UpdatePermission, ok)
	}
}

func mailCredentialPermissions(group Group) bool {
	return group.ViewPermission == policies.PermMailView && group.UpdatePermission == policies.PermMailUpdate
}

func providerCredentialPermissions(group Group) bool {
	return group.ViewPermission == policies.PermProvidersView && group.UpdatePermission == policies.PermProvidersUpdate
}

func sweepLimitPermissions(group Group) bool {
	return group.ViewPermission == policies.PermSweepView && group.UpdatePermission == policies.PermSweepUpdate &&
		group.UpdatePermission != policies.PermSettingsUpdate && group.ViewPermission != policies.PermSettingsUpdate
}

func platformSecretGroupGatedByAdmins(group Group) bool {
	switch group.Name {
	case groupMailSMTP, groupMailSES, groupMailMailgun, groupMailResend, groupMailPostmark:
		return mailCredentialPermissions(group) && platformAdminCoversViewPermission(group.ViewPermission)
	case groupPriceCoinGecko, groupPriceCoinMarketCap, groupPriceCoinAPI,
		groupProviderAlchemy, groupProviderHelius, groupProviderQuickNode, groupProviderEtherscan:
		return providerCredentialPermissions(group) && platformAdminCoversViewPermission(group.ViewPermission)
	default:
		return false
	}
}

func TestSigningSecretIsASecretString(t *testing.T) {
	t.Parallel()

	definition, ok := Find(groupAccountWebhooks, keySigningSecret)
	if !ok || !definition.Secret || definition.Type != TypeString {
		t.Fatalf("signing secret = %+v present %v", definition, ok)
	}
	idle, ok := Find(groupAccountSecurity, keySessionIdleMinutes)
	if !ok || idle.Type != TypeInt {
		t.Fatalf("session idle = %+v present %v", idle, ok)
	}
}

func TestFind_PortIsInt(t *testing.T) {
	t.Parallel()

	definition, ok := Find(groupMailSMTP, keyMailPort)
	if !ok {
		t.Fatal("mail_smtp port is not in the registry")
	}
	if definition.Type != TypeInt || definition.Secret {
		t.Fatalf("port type = %q secret %v", definition.Type, definition.Secret)
	}
}
