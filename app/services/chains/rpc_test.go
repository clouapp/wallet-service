package chains_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

func TestUpdate_RPC_UnknownChainIsNotFoundBeforeTheAdminCheck(t *testing.T) {
	t.Parallel()

	store := &rpcStore{}
	admins := &thresholdAdmins{err: errors.New("admin lookup must not run")}
	service := chainsvc.NewRPC(chainsvc.RPCDeps{
		Store: store, Admins: admins, Activity: &thresholdActivity{}, Sealer: prefixSeal{}, Dialer: &hostDialer{},
	})

	_, err := service.Update(context.Background(), uuid.New(), "missing", thresholdObject(t, `{"rpcUrl":""}`))
	require.ErrorIs(t, err, chainsvc.ErrNotFound)
	require.Empty(t, store.sealed)
}

func TestUpdate_RPC_NonAdminLeavesTheEndpointUnchanged(t *testing.T) {
	t.Parallel()

	store := &rpcStore{chain: ethChain("1", "1", "1"), sealed: "kept"}
	store.chain.RpcURL = "kept"
	service := chainsvc.NewRPC(chainsvc.RPCDeps{
		Store: store, Admins: &thresholdAdmins{}, Activity: &thresholdActivity{}, Sealer: prefixSeal{}, Dialer: &hostDialer{},
	})

	_, err := service.Update(context.Background(), uuid.New(), "eth", thresholdObject(t, `{"rpcUrl":"https://dial.example/v2/token"}`))
	require.ErrorIs(t, err, chainsvc.ErrPlatformForbidden)
	require.Equal(t, "kept", store.sealed)
}

func TestUpdate_RPC_EmptyURLIsNotStored(t *testing.T) {
	t.Parallel()

	store := &rpcStore{chain: ethChain("1", "1", "1"), sealed: "kept"}
	store.chain.RpcURL = "kept"
	activity := &thresholdActivity{}
	service := chainsvc.NewRPC(chainsvc.RPCDeps{
		Store: store, Admins: &thresholdAdmins{allow: true}, Activity: activity, Sealer: prefixSeal{}, Dialer: &hostDialer{},
	})

	_, err := service.Update(context.Background(), uuid.New(), "eth", thresholdObject(t, `{"rpcUrl":"  "}`))
	var invalid *chainsvc.ValidationError
	require.ErrorAs(t, err, &invalid)
	require.Equal(t, []string{"must not be empty"}, invalid.Fields["rpcUrl"])
	require.Equal(t, "kept", store.sealed)
	require.Empty(t, activity.rows)
}

func TestUpdate_RPC_AdminSealsTheURLAndTheDialerSeesTheHost(t *testing.T) {
	t.Parallel()

	const endpoint = "https://dial.example/v2/route-key"
	store := &rpcStore{chain: ethChain("5", "1", "1"), sealed: "env-sealed"}
	store.chain.RpcURL = "env-sealed"
	activity := &thresholdActivity{}
	dialer := &hostDialer{}
	actor := uuid.New()
	service := chainsvc.NewRPC(chainsvc.RPCDeps{
		Store: store, Admins: &thresholdAdmins{allow: true}, Activity: activity, Sealer: prefixSeal{}, Dialer: dialer,
	})

	view, err := service.Update(context.Background(), actor, "eth", thresholdObject(t, `{"rpcUrl":"`+endpoint+`"}`))
	require.NoError(t, err)
	require.True(t, view.RPCURLSet)
	encodedView, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(encodedView), "dial.example")
	require.NotContains(t, string(encodedView), "route-key")
	require.NotContains(t, string(encodedView), "://")

	require.True(t, strings.HasPrefix(store.sealed, "sealed:"))
	opened := strings.TrimPrefix(store.sealed, "sealed:")
	require.Equal(t, endpoint, opened)
	require.NotContains(t, opened, "env-fallback")

	require.Equal(t, "dial.example", dialer.host)
	require.NotContains(t, dialer.host, "route-key")

	require.Len(t, activity.rows, 1)
	require.Nil(t, activity.rows[0].AccountID)
	require.Equal(t, actor, activity.rows[0].ActorUserID)
	require.Equal(t, activitylog.ActionChainsUpdated, activity.rows[0].Action)
	require.Equal(t, "eth", activity.rows[0].TargetID)
	meta, err := activity.rows[0].Metadata.Encode()
	require.NoError(t, err)
	require.Contains(t, meta, `"key":"eth"`)
	require.Contains(t, meta, `"rpc_url"`)
	require.NotContains(t, meta, "dial.example")
	require.NotContains(t, meta, "route-key")
	require.NotContains(t, meta, "http")
	require.NotContains(t, meta, "platform.secret_viewed")
	require.Equal(t, "5", store.chain.GasReadinessThreshold().String())
}

func TestUpdate_RPC_OpenFailureDoesNotIncludeTheURL(t *testing.T) {
	t.Parallel()

	const endpoint = "https://dial.example/v2/route-key"
	store := &rpcStore{chain: ethChain("1", "1", "1")}
	service := chainsvc.NewRPC(chainsvc.RPCDeps{
		Store: store, Admins: &thresholdAdmins{allow: true}, Activity: &thresholdActivity{}, Sealer: brokenOpen{}, Dialer: &hostDialer{},
	})

	_, err := service.Update(context.Background(), uuid.New(), "eth", thresholdObject(t, `{"rpcUrl":"`+endpoint+`"}`))
	require.EqualError(t, err, "open chain rpc")
	require.NotContains(t, err.Error(), "dial.example")
	require.NotContains(t, err.Error(), "route-key")
}

type rpcStore struct {
	chain  *models.Chain
	sealed string
}

func (s *rpcStore) FindByID(_ context.Context, id string) (*models.Chain, error) {
	if s.chain == nil || s.chain.ID != id {
		return nil, models.ErrRepositoryNotFound
	}
	copy := *s.chain
	copy.RpcURL = s.sealed
	return &copy, nil
}

func (s *rpcStore) UpdateRPCURL(_ context.Context, id, sealed string) error {
	if s.chain == nil || s.chain.ID != id || sealed == "" {
		return models.ErrRepositoryNotFound
	}
	s.sealed = sealed
	return nil
}

type prefixSeal struct{}

func (prefixSeal) Seal(plaintext string) (string, error) {
	if plaintext == "" {
		return "", errors.New("empty")
	}
	return "sealed:" + plaintext, nil
}

func (prefixSeal) Open(stored string) (string, error) {
	raw, ok := strings.CutPrefix(stored, "sealed:")
	if !ok || raw == "" {
		return "", errors.New("not sealed")
	}
	return raw, nil
}

type brokenOpen struct{}

func (brokenOpen) Seal(plaintext string) (string, error) {
	return "sealed:" + plaintext, nil
}

func (brokenOpen) Open(stored string) (string, error) {
	return "", errors.New("open failed " + stored)
}

type hostDialer struct {
	host string
}

func (d *hostDialer) ReplaceEndpoint(_ string, endpoint string) (bool, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return false, errors.New("replace endpoint")
	}
	d.host = parsed.Host
	return true, nil
}
