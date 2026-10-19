package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/rpcendpoints"
)

// rpcEndpointsRunner runs one chains:rpc-endpoints invocation; *rpcendpoints.Service
// is the one implementation.
type rpcEndpointsRunner interface {
	Run(ctx context.Context, req rpcendpoints.Request) (rpcendpoints.Report, error)
}

// ChainsRPCEndpoints lists the RPC endpoints of a chain (position, host, enabled,
// health) or changes them. Every endpoint it adds is asked first which network it
// serves. Nothing is written without --apply. No URL is ever printed.
type ChainsRPCEndpoints struct {
	endpoints rpcEndpointsRunner
}

// NewChainsRPCEndpoints wires the command to the endpoint service.
func NewChainsRPCEndpoints(endpoints rpcEndpointsRunner) *ChainsRPCEndpoints {
	if endpoints == nil {
		panic("chains:rpc-endpoints: endpoint service is required")
	}
	return &ChainsRPCEndpoints{endpoints: endpoints}
}

func (c *ChainsRPCEndpoints) Signature() string { return "chains:rpc-endpoints" }

func (c *ChainsRPCEndpoints) Description() string {
	return "List a chain's RPC endpoints (position, host, enabled, health), or add, remove, reorder, enable, disable or replace them after checking that each new one serves the chain's network; dry run unless --apply"
}

func (c *ChainsRPCEndpoints) Extend() command.Extend {
	return command.Extend{
		Category: "chains",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "chain_id", Usage: "chain identifier (e.g. eth, teth, polygon, sol)", Required: true},
		},
		Flags: []command.Flag{
			&command.StringFlag{Name: "add", Usage: "add an endpoint: an http(s) URL or env:NAME (at the end, or at --position)"},
			&command.StringFlag{Name: "position", Usage: "with --add: the position the endpoint takes (1 makes it the primary)"},
			&command.StringFlag{Name: "remove", Usage: "remove the fallback at this position"},
			&command.StringFlag{Name: "enable", Usage: "put the fallback at this position back in use"},
			&command.StringFlag{Name: "disable", Usage: "keep the fallback at this position in the list, unused"},
			&command.StringFlag{Name: "order", Usage: "the current positions in their new order, comma-separated (2,1,3: position 2 becomes the primary)"},
			&command.StringFlag{Name: "set", Usage: "replace the whole list: comma-separated URLs or env:NAME, the primary first"},
			&command.BoolFlag{Name: "defaults", Usage: "append the default fallbacks of the chain's network that the list does not hold"},
			&command.BoolFlag{Name: "probe", Usage: "with the list: ask every endpoint now for its network and head"},
			&command.BoolFlag{Name: "apply", Usage: "write the change (default: print it only)"},
			&command.BoolFlag{Name: "skip-network-check", Usage: "emergency only: write without asking the endpoints which network they serve (logged)"},
		},
	}
}

func (c *ChainsRPCEndpoints) Handle(ctx console.Context) error {
	req, err := rpcEndpointsRequest(ctx)
	if err != nil {
		return fail(ctx, err)
	}
	report, err := c.endpoints.Run(context.Background(), req)
	printReport(ctx, report.Info, report.Warning, nil, nil)
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}

// rpcEndpointsRequest reads the chain and at most one change from the flags.
func rpcEndpointsRequest(ctx console.Context) (rpcendpoints.Request, error) {
	req := rpcendpoints.Request{
		ChainID:          strings.TrimSpace(ctx.ArgumentString("chain_id")),
		Probe:            ctx.OptionBool("probe"),
		Apply:            ctx.OptionBool("apply"),
		SkipNetworkCheck: ctx.OptionBool("skip-network-check"),
	}
	var changes []rpcendpoints.Change
	if url := strings.TrimSpace(ctx.Option("add")); url != "" {
		position, err := optionalPosition(ctx.Option("position"), "--position")
		if err != nil {
			return req, err
		}
		changes = append(changes, rpcendpoints.Change{Action: rpcendpoints.ActionAdd, URL: url, Position: position})
	} else if strings.TrimSpace(ctx.Option("position")) != "" {
		return req, fmt.Errorf("--position goes with --add")
	}
	for flag, action := range map[string]rpcendpoints.Action{
		"remove": rpcendpoints.ActionRemove, "enable": rpcendpoints.ActionEnable, "disable": rpcendpoints.ActionDisable,
	} {
		raw := strings.TrimSpace(ctx.Option(flag))
		if raw == "" {
			continue
		}
		position, err := optionalPosition(raw, "--"+flag)
		if err != nil {
			return req, err
		}
		changes = append(changes, rpcendpoints.Change{Action: action, Position: position})
	}
	if raw := strings.TrimSpace(ctx.Option("order")); raw != "" {
		var order []int
		for _, part := range strings.Split(raw, ",") {
			position, err := optionalPosition(part, "--order")
			if err != nil {
				return req, err
			}
			order = append(order, position)
		}
		changes = append(changes, rpcendpoints.Change{Action: rpcendpoints.ActionOrder, Order: order})
	}
	if raw := strings.TrimSpace(ctx.Option("set")); raw != "" {
		changes = append(changes, rpcendpoints.Change{Action: rpcendpoints.ActionSet, URLs: strings.Split(raw, ",")})
	}
	if ctx.OptionBool("defaults") {
		changes = append(changes, rpcendpoints.Change{Action: rpcendpoints.ActionDefaults})
	}
	if len(changes) > 1 {
		return req, fmt.Errorf("give one change at a time: --add, --remove, --enable, --disable, --order, --set or --defaults")
	}
	if len(changes) == 1 {
		req.Change = &changes[0]
	}
	return req, nil
}

// optionalPosition reads a positive position; an empty value is 0 (not given).
func optionalPosition(raw, flag string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	position, err := strconv.Atoi(raw)
	if err != nil || position < 1 {
		return 0, fmt.Errorf("%s must be a position (1, 2, ...), got %q", flag, raw)
	}
	return position, nil
}
