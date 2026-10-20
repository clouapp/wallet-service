package rpcendpoints

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/egress"
)

func TestApply_RefusesALinkLocalOrMetadataEndpointBeforeAskingIt(t *testing.T) {
	t.Setenv("RPC_ENDPOINT_TEST_METADATA_URL", "http://169.254.170.2/v2/credentials")
	cases := map[string]struct {
		change Change
		reason string
	}{
		"cloud metadata":         {Change{Action: ActionAdd, URL: "http://169.254.169.254/latest/meta-data/"}, egress.ReasonLinkLocal},
		"IPv6 link-local":        {Change{Action: ActionAdd, URL: "http://[fe80::1]:8545"}, egress.ReasonLinkLocal},
		"metadata name":          {Change{Action: ActionAdd, URL: "http://metadata.google.internal/computeMetadata/v1/"}, egress.ReasonMetadata},
		"unspecified":            {Change{Action: ActionAdd, URL: "http://0.0.0.0:8545"}, egress.ReasonReserved},
		"numeric form":           {Change{Action: ActionAdd, URL: "http://0xa9fea9fe/latest"}, egress.ReasonHostName},
		"env reference":          {Change{Action: ActionAdd, URL: "env:RPC_ENDPOINT_TEST_METADATA_URL"}, egress.ReasonLinkLocal},
		"in a whole list":        {Change{Action: ActionSet, URLs: []string{filteredNode, "http://169.254.169.254/"}}, egress.ReasonLinkLocal},
		"as a new primary":       {Change{Action: ActionAdd, URL: "http://100.100.100.200/latest/", Position: 1}, egress.ReasonLinkLocal},
		"AWS IPv6 metadata node": {Change{Action: ActionAdd, URL: "http://[fd00:ec2::254]/latest/"}, egress.ReasonLinkLocal},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, filteredNode)
			_, err := h.service.Apply(context.Background(), models.ChainETH, tc.change, true)
			var refusal *Refusal
			require.ErrorAs(t, err, &refusal)
			assert.Equal(t, tc.reason, refusal.Reason)
			assert.Empty(t, h.verified, "a refused endpoint was asked for its network")
			assert.Zero(t, h.store.replaceWrite+h.store.primaryWrite)
		})
	}
}

func TestApply_AcceptsAPrivateOrLoopbackNode(t *testing.T) {
	for _, url := range []string{"http://10.0.0.5:8545", "http://127.0.0.1:8545", "http://geth:8545", "https://user:secret@node.internal/rpc"} {
		h := newHarness(t, filteredNode)
		_, err := h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionAdd, URL: url}, false)
		var refusal *Refusal
		require.ErrorAs(t, err, &refusal, url)
		// The node is asked (and, unknown to the fake, does not answer): the guard let it through.
		assert.Equal(t, []string{url}, h.verified, url)
	}
}

func TestApply_AcceptsALinkLocalNodeWithTheFlag(t *testing.T) {
	h := newHarness(t, filteredNode)
	deps := h.deps
	deps.AllowLinkLocal = true
	h.service = NewService(deps)
	_, _ = h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionAdd, URL: "http://169.254.10.20:8545"}, false)
	assert.Equal(t, []string{"http://169.254.10.20:8545"}, h.verified)
}
