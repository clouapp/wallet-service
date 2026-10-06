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

// TestNoDeadEventsOrListeners refuses an event type nobody dispatches and a
// listener nobody registers (.ai/guidelines/queues-and-workers.md).
func TestNoDeadEventsOrListeners(t *testing.T) {
	module := sharedModule(t)
	dispatched := dispatchedEventTypes(module)
	registeredListeners := registeredListenerNames(t, module)
	for _, name := range eventTypeNames(module) {
		if !dispatched[name] {
			t.Errorf("%s is an event nobody dispatches", name)
		}
	}
	for _, name := range listenerTypeNames(module) {
		if !registeredListeners[name] {
			t.Errorf("%s is a listener nobody registers", name)
		}
	}
}

func registeredListenerNames(t *testing.T, module *Module) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	for _, file := range module.ProductionFiles("bootstrap") {
		if file.Path != "bootstrap/app.go" {
			continue
		}
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
				listeners, ok := pair.Value.(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, listener := range listeners.Elts {
					if name := compositeTypeName(listener); name != "" {
						found[name] = true
					}
				}
			}
			return false
		})
	}
	if len(found) == 0 {
		t.Fatal("no registered listeners")
	}
	return found
}

func eventTypeNames(module *Module) []string {
	return methodReceiverNames(module, func(fn *ast.FuncDecl) bool {
		return fn.Name.Name == "Handle" && isDomainEventHandle(fn.Type)
	})
}

func listenerTypeNames(module *Module) []string {
	methods := map[string]map[string]bool{}
	for _, file := range module.ProductionFiles() {
		for _, decl := range file.AST.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name == nil {
				continue
			}
			name := receiverTypeName(fn)
			if name == "" {
				continue
			}
			switch fn.Name.Name {
			case "Signature", "Handle":
				if methods[name] == nil {
					methods[name] = map[string]bool{}
				}
				methods[name][fn.Name.Name] = true
			case "Queue":
				if !isListenerQueue(fn.Type) {
					continue
				}
				if methods[name] == nil {
					methods[name] = map[string]bool{}
				}
				methods[name]["Queue"] = true
			}
		}
	}
	names := make([]string, 0)
	for name, set := range methods {
		if set["Signature"] && set["Queue"] && set["Handle"] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func methodReceiverNames(module *Module, match func(*ast.FuncDecl) bool) []string {
	found := map[string]bool{}
	for _, file := range module.ProductionFiles() {
		for _, decl := range file.AST.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name == nil || !match(fn) {
				continue
			}
			if name := receiverTypeName(fn); name != "" {
				found[name] = true
			}
		}
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func isDomainEventHandle(fnType *ast.FuncType) bool {
	if fnType == nil || fnType.Params == nil || len(fnType.Params.List) != 1 {
		return false
	}
	if !isEventArgSlice(fnType.Params.List[0].Type) {
		return false
	}
	if fnType.Results == nil || len(fnType.Results.List) != 2 {
		return false
	}
	return isEventArgSlice(fnType.Results.List[0].Type) && isIdentName(fnType.Results.List[1].Type, "error")
}

func isListenerQueue(fnType *ast.FuncType) bool {
	if fnType == nil || fnType.Params == nil || len(fnType.Params.List) != 1 {
		return false
	}
	_, ok := fnType.Params.List[0].Type.(*ast.Ellipsis)
	return ok
}

func isEventArgSlice(expr ast.Expr) bool {
	array, ok := expr.(*ast.ArrayType)
	if !ok || array.Len != nil {
		return false
	}
	selector, ok := array.Elt.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Arg"
}

func isIdentName(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

func receiverTypeName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
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
