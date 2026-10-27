package settings

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// envImport is one environment variable a platform setting replaces, and how its
// value becomes the setting's: convert returns ok false to leave the field out (a
// value that keeps the default, or one the setting cannot hold).
type envImport struct {
	group   string
	key     string
	env     string
	convert func(raw string) (any, bool)
}

func importString(raw string) (any, bool) { return raw, raw != "" }

func importList(raw string) (any, bool) {
	items := []any{}
	for _, part := range strings.Split(raw, ",") {
		if item := strings.TrimSpace(part); item != "" {
			items = append(items, item)
		}
	}
	return items, true
}

func importPositive(raw string) (any, bool) {
	n, err := strconv.Atoi(raw)
	return n, err == nil && n > 0
}

func importBool(raw string) (any, bool) {
	switch strings.ToLower(raw) {
	case "true", "1":
		return true, true
	case "false", "0":
		return false, true
	}
	return nil, false
}

// importThrottle maps a THROTTLE_* value to http_throttle: the environment's 0
// (off) is the setting's -1, since its 0 keeps the environment value.
func importThrottle(raw string) (any, bool) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return nil, false
	}
	if n == 0 {
		return -1, true
	}
	return n, true
}

func envImports() []envImport {
	imports := []envImport{
		{GroupLocalWorkers, KeyDepositScanChains, "LOCAL_DEPOSIT_SCAN_CHAINS", importList},
		{groupDepositSignatures, keySignatureChains, "DEPOSIT_SIGNATURE_CHAINS", importList},
		{groupDepositScan, keyAlertLagBlocks, "DEPOSIT_SCAN_ALERT_LAG_BLOCKS", importPositive},
		{groupDepositScan, keyAlertStaleMinutes, "DEPOSIT_SCAN_ALERT_STALE_MINUTES", importPositive},
		{GroupXRPStream, keyStreamEnabled, "LOCAL_XRP_STREAM_ENABLED", importBool},
		{GroupXRPStream, keyStreamChain, "LOCAL_XRP_STREAM_CHAIN", importString},
		{GroupXRPStream, KeyStreamURL, "LOCAL_XRP_STREAM_URL", importString},
		{GroupXRPStream, keyStreamAddressRefresh, "LOCAL_XRP_STREAM_ADDRESS_REFRESH_SECONDS", importPositive},
		{GroupProviderTron, keyProviderAPIKey, "TRON_API_KEY", importString},
		{GroupWithdrawals, keyStaleAfterHours, "WITHDRAWAL_STALE_AFTER_HOURS", importPositive},
		{GroupPlatformURLs, keyFrontendURL, "APP_FRONTEND_URL", importString},
	}
	for _, entry := range throttleKeys {
		imports = append(imports, envImport{GroupHTTPThrottle, entry.key, entry.env, importThrottle})
	}
	for _, chainID := range UTXOFallbackChains {
		prefix := strings.ToUpper(chainID)
		imports = append(imports,
			envImport{UTXOFallbackGroup(chainID), KeyFallbackRPCURLs, prefix + "_FALLBACK_RPC_URL", importString},
			envImport{UTXOFallbackGroup(chainID), keyFallbackAPIKey, prefix + "_FALLBACK_RPC_API_KEY", importString},
			envImport{UTXOFallbackGroup(chainID), keyFallbackTatumDataAPI, prefix + "_TATUM_DATA_API_URL", importString},
		)
	}
	return imports
}

// EnvImportField is one field of an import plan: the variable it comes from and
// whether it is written. Value is never part of it.
type EnvImportField struct {
	Key    string
	Env    string
	Secret bool
	// Skipped says why the field is left out ("" when it is written).
	Skipped string
}

// EnvImportGroup is one group of an import plan.
type EnvImportGroup struct {
	Group  string
	Fields []EnvImportField
	// Written is true once an --apply save of the group succeeded.
	Written bool
}

// ImportPlatformEnv copies into the platform settings the environment values of
// the variables a platform group replaces (envImports), as actorID (a platform
// admin) would save them: through SavePlatform, so every value is validated, a
// secret sealed, the activity log and the cache handled. A variable that is unset
// or blank, a value the setting would treat as its default, and a field a row
// already stores (unless overwrite) are left out. Without apply nothing is written.
// lookup reads one variable; values never leave this function except into the save.
func (s *Service) ImportPlatformEnv(ctx context.Context, actorID uuid.UUID, lookup func(string) (string, bool), apply, overwrite bool) ([]EnvImportGroup, error) {
	if s == nil {
		return nil, errServiceRequired
	}
	if ctx == nil || lookup == nil {
		return nil, fmt.Errorf("settings env import: context and lookup are required")
	}
	var order []string
	plans := map[string]*EnvImportGroup{}
	bodies := map[string]map[string]any{}
	stored := map[string]map[string]string{}
	for _, entry := range envImports() {
		definition, ok := Find(entry.group, entry.key)
		if !ok {
			return nil, fmt.Errorf("settings env import: %s.%s is not in the registry", entry.group, entry.key)
		}
		plan, seen := plans[entry.group]
		if !seen {
			plan = &EnvImportGroup{Group: entry.group}
			plans[entry.group] = plan
			order = append(order, entry.group)
			values, err := s.listPlatformValues(ctx, entry.group)
			if err != nil {
				return nil, err
			}
			stored[entry.group] = values
		}
		field := EnvImportField{Key: entry.key, Env: entry.env, Secret: definition.Secret}
		raw, set := lookup(entry.env)
		raw = strings.TrimSpace(raw)
		value, usable := entry.convert(raw)
		_, already := stored[entry.group][entry.key]
		switch {
		case !set || raw == "":
			field.Skipped = "not set"
		case !usable:
			field.Skipped = "keeps the default"
		case already && !overwrite:
			field.Skipped = "already stored"
		default:
			if bodies[entry.group] == nil {
				bodies[entry.group] = map[string]any{}
			}
			bodies[entry.group][entry.key] = value
		}
		plan.Fields = append(plan.Fields, field)
	}
	if body := bodies[GroupProviderTron]; body != nil {
		// A key copied from TRON_API_KEY is in use, as it was.
		body[keyProviderEnabled] = true
		plans[GroupProviderTron].Fields = append(plans[GroupProviderTron].Fields, EnvImportField{Key: keyProviderEnabled, Env: "TRON_API_KEY"})
	}
	out := make([]EnvImportGroup, 0, len(order))
	for _, name := range order {
		plan := plans[name]
		if apply && len(bodies[name]) > 0 {
			if _, err := s.SavePlatform(ctx, actorID, name, bodies[name]); err != nil {
				return append(out, *plan), fmt.Errorf("settings env import: save %s: %w", name, err)
			}
			plan.Written = true
		}
		out = append(out, *plan)
	}
	return slices.Clip(out), nil
}
