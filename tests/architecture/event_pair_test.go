package architecture

import (
	"go/ast"
	"sort"
	"testing"
)

// TestEveryRegisteredEventHasADispatcherAndAListener refuses a Goravel event
// that is registered without a listener or without a Job dispatch
// (.ai/guidelines/queues-and-workers.md).
func TestEveryRegisteredEventHasADispatcherAndAListener(t *testing.T) {
	module := sharedModule(t)
	registered := registeredEvents(t, module)
	if len(registered) == 0 {
		t.Fatal("no registered events")
	}
	dispatched := dispatchedEventTypes(module)
	names := make([]string, 0, len(registered))
	for name := range registered {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if registered[name] == 0 {
			t.Errorf("%s is registered without a listener", name)
		}
		if !dispatched[name] {
			t.Errorf("%s is registered without a dispatcher", name)
		}
	}
}

func registeredEvents(t *testing.T, module *Module) map[string]int {
	t.Helper()
	var file *SourceFile
	for _, candidate := range module.ProductionFiles("bootstrap") {
		if candidate.Path == "bootstrap/app.go" {
			file = candidate
			break
		}
	}
	if file == nil {
		t.Fatal("bootstrap/app.go was not parsed")
	}
	found := map[string]int{}
	ast.Inspect(file.AST, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok || !isEventListenerMap(lit.Type) {
			return true
		}
		for _, elt := range lit.Elts {
			pair, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			name := compositeTypeName(pair.Key)
			if name == "" {
				continue
			}
			found[name] = listenerCount(pair.Value)
		}
		return false
	})
	return found
}

func isEventListenerMap(expr ast.Expr) bool {
	mapType, ok := expr.(*ast.MapType)
	if !ok {
		return false
	}
	return selectorName(mapType.Key) == "Event" && selectorName(mapType.Value) == "Listener"
}

func listenerCount(expr ast.Expr) int {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return 0
	}
	return len(lit.Elts)
}

func dispatchedEventTypes(module *Module) map[string]bool {
	found := map[string]bool{}
	for _, file := range module.ProductionFiles() {
		if file.Path == "bootstrap/app.go" {
			continue
		}
		ast.Inspect(file.AST, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !isJobCall(call) || len(call.Args) == 0 {
				return true
			}
			if name := compositeTypeName(call.Args[0]); name != "" {
				found[name] = true
			}
			return true
		})
	}
	return found
}

func isJobCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Job"
}

func compositeTypeName(expr ast.Expr) string {
	if unary, ok := expr.(*ast.UnaryExpr); ok {
		expr = unary.X
	}
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return ""
	}
	switch typed := lit.Type.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	default:
		return ""
	}
}

func selectorName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	case *ast.ArrayType:
		return selectorName(typed.Elt)
	default:
		return ""
	}
}
