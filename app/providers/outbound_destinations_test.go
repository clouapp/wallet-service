package providers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWebhook_DestinationPolicy_AllowsLoopbackOnlyInTheTestSuites(t *testing.T) {
	cases := []struct {
		name     string
		settings outboundSettings
		loopback bool
		warning  string
	}{
		{"the suites", outboundSettings{AppEnv: "testing", Env: "testing", Environment: "testing"}, true, "loopback webhook receivers are allowed"},
		{"a local e2e run", outboundSettings{AppEnv: " Testing ", Env: "dev", Environment: "development"}, true, "loopback webhook receivers are allowed"},
		{"testing on a prod host", outboundSettings{AppEnv: "testing", Env: "prod"}, false, "refused on a production-like deployment"},
		{"testing on a production host", outboundSettings{AppEnv: "testing", Environment: "Production"}, false, "refused on a production-like deployment"},
		{"testing on staging", outboundSettings{AppEnv: "testing", Environment: "staging"}, false, "refused on a production-like deployment"},
		{"testing on stage", outboundSettings{AppEnv: "testing", Env: "stage"}, false, "refused on a production-like deployment"},
		{"testing on Lambda", outboundSettings{AppEnv: "testing", Env: "dev", LambdaFunction: "vault-webhook-worker-dev"}, false, "refused on a production-like deployment"},
		{"local", outboundSettings{AppEnv: "local", Env: "dev"}, false, ""},
		{"production", outboundSettings{AppEnv: "production", Env: "prod", Environment: "production"}, false, ""},
		{"no environment at all", outboundSettings{}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy, warnings := webhookDestinationPolicy(tc.settings)
			assert.Equal(t, tc.loopback, policy.AllowLoopback)
			assert.False(t, policy.AllowPrivate || policy.AllowLinkLocal || policy.AllowUserinfo, "a webhook policy relaxes nothing else")
			if tc.warning == "" {
				assert.Empty(t, warnings)
				return
			}
			assert.Len(t, warnings, 1)
			assert.Contains(t, strings.Join(warnings, "\n"), tc.warning)
		})
	}
}

func TestWebhook_DestinationPolicy_ReadsTheHTTPFlagAndThePortList(t *testing.T) {
	policy, warnings := webhookDestinationPolicy(outboundSettings{AppEnv: "production", WebhookAllowHTTP: true, WebhookAllowedPorts: " 443, 8443 "})
	assert.True(t, policy.AllowHTTP)
	assert.Equal(t, []int{443, 8443}, policy.AllowedPorts)
	assert.Empty(t, warnings)

	policy, _ = webhookDestinationPolicy(outboundSettings{AppEnv: "production"})
	assert.False(t, policy.AllowHTTP)
	assert.Nil(t, policy.AllowedPorts, "no list: any port but the refused ones")

	// A list that cannot be read keeps what it can, and never opens every port.
	policy, warnings = webhookDestinationPolicy(outboundSettings{AppEnv: "production", WebhookAllowedPorts: "443,https,70000"})
	assert.Equal(t, []int{443}, policy.AllowedPorts)
	assert.Len(t, warnings, 2)
	policy, warnings = webhookDestinationPolicy(outboundSettings{AppEnv: "production", WebhookAllowedPorts: "https"})
	assert.Equal(t, []int{443}, policy.AllowedPorts)
	assert.Len(t, warnings, 1)
}

func TestRPC_DestinationPolicy_FollowsTheLinkLocalFlag(t *testing.T) {
	assert.False(t, rpcDestinationPolicy(outboundSettings{}).AllowLinkLocal)
	assert.True(t, rpcDestinationPolicy(outboundSettings{RPCAllowLinkLocal: true}).AllowLinkLocal)
	assert.True(t, rpcDestinationPolicy(outboundSettings{}).AllowPrivate, "operators run their own nodes")
}
