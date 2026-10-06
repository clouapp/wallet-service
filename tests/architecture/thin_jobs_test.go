package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestJobsAreThin refuses a job handler that does more than decode a typed
// payload and call one service method (.ai/guidelines/queues-and-workers.md).
func TestJobsAreThin(t *testing.T) {
	module := sharedModule(t)
	var violations []string
	var handlers int
	for _, file := range module.ProductionFiles("app/jobs") {
		for _, decl := range file.AST.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "Handle" || fn.Body == nil {
				continue
			}
			handlers++
			for _, problem := range jobHandleProblems(fn) {
				violations = append(violations, file.Path+" "+problem)
			}
		}
	}
	if handlers != 6 {
		t.Fatalf("job handlers = %d, want 6", handlers)
	}
	for _, line := range violations {
		t.Errorf("fat job: %s", line)
	}
}

func TestJobHandleProblems_RefusesASwitchAndASecondCall(t *testing.T) {
	fn := parseHandle(t, `package jobs
func (j *Job) Handle(args ...any) error {
	payload, err := decodeWalletPayload(args)
	if err != nil {
		return err
	}
	switch payload {
	default:
		_ = j.wallets.FindByID(payload)
		_ = j.wallets.RefreshBalances(payload)
		return j.wallets.RefreshTokens(payload)
	}
}`)
	problems := jobHandleProblems(fn)
	joined := strings.Join(problems, "; ")
	if !strings.Contains(joined, "switch") || !strings.Contains(joined, "FindByID") || !strings.Contains(joined, "service calls = 2") {
		t.Fatalf("problems = %s", joined)
	}
}

func parseHandle(t *testing.T, src string) *ast.FuncDecl {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "job.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "Handle" {
			return fn
		}
	}
	t.Fatal("no Handle")
	return nil
}

var walletServiceCalls = map[string]bool{
	"RefreshBalances":     true,
	"RefreshTransactions": true,
	"RefreshTokens":       true,
	"RefreshUTXOs":        true,
	"ReconcileWallet":     true,
	"Send":                true,
}

func jobHandleProblems(fn *ast.FuncDecl) []string {
	var problems []string
	decodeCalls := 0
	serviceCalls := 0
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.SwitchStmt, *ast.TypeSwitchStmt:
			problems = append(problems, "contains a switch")
		case *ast.CallExpr:
			switch fun := n.Fun.(type) {
			case *ast.Ident:
				if strings.HasPrefix(fun.Name, "decode") {
					decodeCalls++
					break
				}
				problems = append(problems, "calls "+fun.Name)
			case *ast.SelectorExpr:
				name := fun.Sel.Name
				if name == "Background" || name == "mailer" {
					break
				}
				if walletServiceCalls[name] {
					serviceCalls++
					break
				}
				problems = append(problems, "calls "+name)
			}
		}
		return true
	})
	if decodeCalls != 1 {
		problems = append(problems, fmt.Sprintf("decode calls = %d", decodeCalls))
	}
	if serviceCalls != 1 {
		problems = append(problems, fmt.Sprintf("service calls = %d", serviceCalls))
	}
	return problems
}
