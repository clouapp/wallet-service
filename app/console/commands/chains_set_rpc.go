package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/chains"
)

// ChainsSetRPC replaces the sealed rpc_url of one chain. The endpoint is
// encrypted before it is stored and is never printed.
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
	}
}

func (c *ChainsSetRPC) Handle(ctx console.Context) error {
	line, err := c.rpc.Replace(context.Background(), ctx.ArgumentString("chain_id"), ctx.ArgumentString("url"))
	if err != nil {
		return fail(ctx, err)
	}
	ctx.Info(line)
	return nil
}
