package providers

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// errActiveTokensNotLoaded means Boot has not read the token catalog yet.
var errActiveTokensNotLoaded = errors.New("active tokens were not loaded during boot")

var (
	activeTokenMu     sync.Mutex
	activeTokensReady bool
	activeTokenRows   []models.Token
)

type activeTokenReader interface {
	FindActive(ctx context.Context) ([]models.Token, error)
}

// loadActiveTokens reads the active token catalog in Boot.
// The chain registry binding registers that catalog and does not query tokens.
// A read failure is logged and leaves the catalog empty. The process stays up.
func loadActiveTokens(app foundation.Application) {
	if app == nil {
		slog.Error("failed to load tokens from DB", "error", errActiveTokensNotLoaded)
		rememberActiveTokens(nil)
		return
	}
	repo, err := resolve[*repositories.TokenRepository](app)
	if err != nil || repo == nil {
		if err == nil {
			err = errActiveTokensNotLoaded
		}
		slog.Error("failed to load tokens from DB", "error", err)
		rememberActiveTokens(nil)
		return
	}
	readActiveTokens(repo)
}

func readActiveTokens(repo activeTokenReader) {
	if repo == nil {
		slog.Error("failed to load tokens from DB", "error", errActiveTokensNotLoaded)
		rememberActiveTokens(nil)
		return
	}
	rows, err := repo.FindActive(context.Background())
	if err != nil {
		slog.Error("failed to load tokens from DB", "error", err)
		rememberActiveTokens(nil)
		return
	}
	rememberActiveTokens(rows)
}

func rememberActiveTokens(rows []models.Token) {
	copied := make([]models.Token, len(rows))
	copy(copied, rows)
	activeTokenMu.Lock()
	activeTokenRows = copied
	activeTokensReady = true
	activeTokenMu.Unlock()
}

func bootedActiveTokens() ([]models.Token, bool) {
	activeTokenMu.Lock()
	defer activeTokenMu.Unlock()
	if !activeTokensReady {
		return nil, false
	}
	out := make([]models.Token, len(activeTokenRows))
	copy(out, activeTokenRows)
	return out, true
}

// registerActiveTokens installs the Boot catalog on the chain registry and
// returns the same tokens grouped for each chain adapter.
func registerActiveTokens(reg *chainpkg.Registry, rows []models.Token) map[string][]types.Token {
	tokensByChain := make(map[string][]types.Token)
	for _, row := range rows {
		tok := types.Token{
			Symbol:   row.Symbol,
			Name:     row.Name,
			Contract: row.ContractAddress,
			Decimals: uint8(row.Decimals),
			ChainID:  row.ChainID,
		}
		tokensByChain[row.ChainID] = append(tokensByChain[row.ChainID], tok)
		if reg != nil {
			reg.RegisterToken(tok)
		}
	}
	return tokensByChain
}
