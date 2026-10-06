package chainregistry

import "github.com/macrowallets/waas/app/services/chain"

// ErrUnknownChain is a chain the registry does not know.
// A store failure is a different error; tell them apart with errors.Is.
var ErrUnknownChain = chain.ErrUnknownChain
