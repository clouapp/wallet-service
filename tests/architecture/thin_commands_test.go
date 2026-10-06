package architecture

import (
	"fmt"
	"go/ast"
	"strings"
	"testing"
)

// commandExceptions stay fat on purpose: withdraw:preflight keeps the service
// lookup macro-e2e does not own, and wallets:export-keys stays the zip path.
var commandExceptions = map[string]bool{
	"app/console/commands/withdraw_preflight.go":  true,
	"app/console/commands/wallets_export_keys.go": true,
}

// TestCommandsAreThin refuses an artisan command that queries, calls more
// than one service method, or returns an error without fail
// (.ai/guidelines/queues-and-workers.md).
func TestCommandsAreThin(t *testing.T) {
	module := sharedModule(t)
	var violations []string
	var handlers int
	for _, file := range module.ProductionFiles("app/console/commands") {
		if commandExceptions[file.Path] {
			continue
		}
		handlers += commandHandles(file.AST)
		for _, problem := range commandFileProblems(file.AST) {
			violations = append(violations, file.Path+" "+problem)
		}
	}
	if handlers != 13 {
		t.Fatalf("thin command handlers = %d, want 13", handlers)
	}
	for _, line := range violations {
		t.Errorf("fat command: %s", line)
	}
}

func commandHandles(file *ast.File) int {
	count := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv != nil && fn.Name.Name == "Handle" && fn.Body != nil {
			count++
		}
	}
	return count
}

func commandFileProblems(file *ast.File) []string {
	var problems []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if fn.Name.Name == "Handle" && fn.Recv != nil {
			problems = append(problems, commandHandleProblems(fn)...)
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "Query", "Exec", "MustMake", "FindByID", "FindByChainAndAddress", "FindByChainAndTxHash", "UpdateRPCURL":
				problems = append(problems, "queries via "+sel.Sel.Name)
			}
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "facades" {
				problems = append(problems, "calls facades."+sel.Sel.Name)
			}
			return true
		})
	}
	return problems
}

func commandHandleProblems(fn *ast.FuncDecl) []string {
	var problems []string
	receiver := receiverIdent(fn)
	serviceCalls := 0
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		switch n := node.(type) {
		case *ast.ReturnStmt:
			if !returnsNilOrFail(n) {
				problems = append(problems, "returns without fail")
			}
		case *ast.CallExpr:
			if serviceCall(n, receiver) {
				serviceCalls++
			}
		}
		return true
	})
	if serviceCalls != 1 {
		problems = append(problems, fmt.Sprintf("service calls = %d", serviceCalls))
	}
	return problems
}

func receiverIdent(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 || len(fn.Recv.List[0].Names) == 0 {
		return ""
	}
	return fn.Recv.List[0].Names[0].Name
}

func serviceCall(call *ast.CallExpr, receiver string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	inner, ok := sel.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := inner.X.(*ast.Ident)
	return ok && ident.Name == receiver
}

func returnsNilOrFail(stmt *ast.ReturnStmt) bool {
	if len(stmt.Results) != 1 {
		return false
	}
	switch result := stmt.Results[0].(type) {
	case *ast.Ident:
		return result.Name == "nil"
	case *ast.CallExpr:
		ident, ok := result.Fun.(*ast.Ident)
		return ok && ident.Name == "fail"
	default:
		return false
	}
}

func TestCommandHandleProblems_RefusesAQueryAndASecondCall(t *testing.T) {
	fn := parseHandle(t, `package commands
func (c *Cmd) Handle(ctx console.Context) error {
	row, err := c.store.FindByID(ctx, id)
	if err != nil {
		return err
	}
	return c.store.Save(row)
}`)
	joined := strings.Join(commandHandleProblems(fn), "; ")
	if !strings.Contains(joined, "service calls = 2") || !strings.Contains(joined, "returns without fail") {
		t.Fatalf("problems = %s", joined)
	}
}
