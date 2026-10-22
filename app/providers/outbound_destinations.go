package providers

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/pkg/egress"
)

// appEnvTesting is the APP_ENV of the test suites (and of a local end-to-end run),
// the one environment whose webhook receivers may be on loopback.
const appEnvTesting = "testing"

// productionLikeEnvironments are the ENV, ENVIRONMENT or APP_ENV values of a
// long-lived deployment, where APP_ENV=testing never opens loopback.
var productionLikeEnvironments = map[string]bool{"prod": true, "production": true, "staging": true, "stage": true}

// outboundSettings are the settings the destination policies come from.
type outboundSettings struct {
	AppEnv              string
	Env                 string
	Environment         string
	LambdaFunction      string
	WebhookAllowHTTP    bool
	WebhookAllowedPorts string
	RPCAllowLinkLocal   bool
}

func outboundSettingsFromConfig() outboundSettings {
	cfg := facades.Config()
	return outboundSettings{
		AppEnv:              cfg.GetString("app.env"),
		Env:                 cfg.GetString("vault.deployment.env"),
		Environment:         cfg.GetString("vault.deployment.environment"),
		LambdaFunction:      cfg.GetString("vault.deployment.lambda_function"),
		WebhookAllowHTTP:    cfg.GetBool("vault.outbound.webhook_allow_http"),
		WebhookAllowedPorts: cfg.GetString("vault.outbound.webhook_allowed_ports"),
		RPCAllowLinkLocal:   cfg.GetBool("vault.outbound.rpc_allow_link_local"),
	}
}

// webhookDestinationPolicy is the policy of a webhook receiver: https to a public
// host; http too with WEBHOOK_ALLOW_HTTP; only the ports of WEBHOOK_ALLOWED_PORTS
// when it is set; loopback only under APP_ENV=testing, and never on a
// production-like deployment (ENV, ENVIRONMENT or APP_ENV prod, production,
// staging or stage, or a process on Lambda: the suites never run there). The
// warnings are logged at boot.
func webhookDestinationPolicy(settings outboundSettings) (egress.Policy, []string) {
	policy := egress.Policy{AllowHTTP: settings.WebhookAllowHTTP}
	var warnings []string
	policy.AllowedPorts, warnings = allowedPorts(settings.WebhookAllowedPorts)
	if normalizedEnvironment(settings.AppEnv) != appEnvTesting {
		return policy, warnings
	}
	if productionLike(settings) {
		return policy, append(warnings, "APP_ENV=testing: loopback webhook receivers stay refused on a production-like deployment (ENV or ENVIRONMENT is prod, production, staging or stage, or the process runs on Lambda)")
	}
	policy.AllowLoopback = true
	return policy, append(warnings, "APP_ENV=testing: loopback webhook receivers are allowed; never run a long-lived service with it")
}

// rpcDestinationPolicy is the policy of an RPC endpoint (egress.RPCPolicy), with
// link-local and metadata destinations only under RPC_ALLOW_LINK_LOCAL.
func rpcDestinationPolicy(settings outboundSettings) egress.Policy {
	return egress.RPCPolicy(settings.RPCAllowLinkLocal)
}

var rpcDestinations = sync.OnceValue(func() *egress.Guard {
	return egress.NewGuard(rpcDestinationPolicy(outboundSettingsFromConfig()), nil)
})

// RPCDestinations is the one guard of the RPC endpoints, built once from the config:
// every RPC write checks the URL it would dial with it, and every connection of the
// shared provider transport (pkg/httpclient) opens through its DialContext.
func RPCDestinations() *egress.Guard {
	return rpcDestinations()
}

// newWebhookDestinations is the guard of every webhook URL, logging the warnings of
// its policy.
func newWebhookDestinations() *egress.Guard {
	policy, warnings := webhookDestinationPolicy(outboundSettingsFromConfig())
	for _, warning := range warnings {
		slog.Warn("webhook destinations: " + warning)
	}
	return egress.NewGuard(policy, nil)
}

func productionLike(settings outboundSettings) bool {
	if strings.TrimSpace(settings.LambdaFunction) != "" {
		return true
	}
	for _, value := range []string{settings.AppEnv, settings.Env, settings.Environment} {
		if productionLikeEnvironments[normalizedEnvironment(value)] {
			return true
		}
	}
	return false
}

func normalizedEnvironment(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// allowedPorts reads WEBHOOK_ALLOWED_PORTS (comma-separated). An entry that is not
// a port is dropped with a warning; a list that was set and has none left allows
// 443 only, so a typo never opens every port.
func allowedPorts(raw string) ([]int, []string) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var ports []int
	var warnings []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		port, err := strconv.Atoi(entry)
		if err != nil || port < 1 || port > 65535 {
			warnings = append(warnings, fmt.Sprintf("WEBHOOK_ALLOWED_PORTS entry %q is not a port; ignored", entry))
			continue
		}
		ports = append(ports, port)
	}
	if len(ports) == 0 {
		ports = []int{443}
	}
	return ports, warnings
}
