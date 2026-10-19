package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/chains"
)

// ChainsSetRPC replaces the sealed rpc_url of one chain after asking it which
// network it serves. The endpoint is encrypted before it is stored and is never
// printed.
type ChainsSetRPC struct {
	rpc *chains.ReplaceRPC
}

// NewChainsSetRPC wires the command to the chain endpoint service.
func NewChainsSetRPC(rpc *chains.ReplaceRPC) *ChainsSetRPC {
	return &ChainsSetRPC{rpc: rpc}
}

func (c *ChainsSetRPC) Signature() string { return "chains:set-rpc" }

func (c *ChainsSetRPC) Description() string {
	return "Update the encrypted rpc_url of a chain row (e.g. after rotating providers)"
}

func (c *ChainsSetRPC) Extend() command.Extend {
	return command.Extend{
		Category: "chains",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "chain_id", Usage: "chain identifier (e.g. eth, teth, polygon, sol)", Required: true},
			&command.ArgumentString{Name: "url", Usage: "new RPC URL, or env:NAME to read it from an environment variable (encrypted at rest)", Required: true},
		},
		Flags: []command.Flag{
			&command.BoolFlag{Name: "skip-network-check", Usage: "emergency only: write the endpoint without asking which network it serves (logged)"},
		},
	}
}

func (c *ChainsSetRPC) Handle(ctx console.Context) error {
	skip := ctx.OptionBool("skip-network-check")
	line, err := c.rpc.Replace(context.Background(), ctx.ArgumentString("chain_id"), ctx.ArgumentString("url"), skip)
	if err != nil {
		return fail(ctx, err)
	}
	if skip {
		ctx.Warning(line)
		return nil
	}
	ctx.Info(line)
	return nil
}
