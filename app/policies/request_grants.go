package policies

import (
	"context"
	"sync"
)

// requestGrantsKey is the context key for one request's role and catalog grants.
// Policies do not import the HTTP layer; middleware stores the value.
type requestGrantsKey struct{}

// RequestGrantsKey is the key middleware uses when it stores the request grants.
func RequestGrantsKey() any {
	return requestGrantsKey{}
}

// requestGrantLoad is the role and its catalog grants for one request.
// There is no permission-override store, so the sets are the code catalog.
// Scope middleware fills it before a policy decides.
type requestGrantLoad struct {
	mu      sync.Mutex
	loaded  bool
	account Grants
	wallet  Grants
}

// AttachRequestGrants is the filled value middleware stores after it has
// already loaded an active membership. A missing or suspended membership
// never reaches this call.
func AttachRequestGrants(role string) any {
	load := &requestGrantLoad{}
	load.remember(role)
	return load
}

func emptyRequestGrants() any {
	return &requestGrantLoad{}
}

func grantLoad(ctx context.Context) *requestGrantLoad {
	if ctx == nil {
		return nil
	}
	load, _ := ctx.Value(RequestGrantsKey()).(*requestGrantLoad)
	return load
}

// AccountGrants is the account catalog stored for this request.
// The boolean is false when this request has not resolved a membership yet.
func AccountGrants(ctx context.Context) (Grants, bool) {
	load := grantLoad(ctx)
	if load == nil {
		return nil, false
	}
	return load.accountGrants()
}

// WalletRequestGrants is the wallet catalog stored for this request.
// The boolean is false when this request has not resolved a membership yet.
func WalletRequestGrants(ctx context.Context) (Grants, bool) {
	load := grantLoad(ctx)
	if load == nil {
		return nil, false
	}
	return load.walletGrants()
}

func (g *requestGrantLoad) remember(role string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.loaded {
		return
	}
	g.loaded = true
	g.account = AccountRoleGrants(role)
	g.wallet = WalletGrants(role)
}

func (g *requestGrantLoad) accountGrants() (Grants, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.loaded {
		return nil, false
	}
	return cloneGrants(g.account), true
}

func (g *requestGrantLoad) walletGrants() (Grants, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.loaded {
		return nil, false
	}
	return cloneGrants(g.wallet), true
}

func cloneGrants(in Grants) Grants {
	if len(in) == 0 {
		return nil
	}
	out := make(Grants, len(in))
	for permission := range in {
		out[permission] = struct{}{}
	}
	return out
}
