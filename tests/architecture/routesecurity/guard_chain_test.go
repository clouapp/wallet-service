package routesecurity_test

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/macrowallets/waas/tests/architecture"
)

// skippedGuards are middleware that run on a route and are not authorization.
var skippedGuards = map[string]bool{
	"CacheControl": true,
	"Cors":         true,
	"Throttle":     true,
}

// routeRegistrationFiles are the only files that attach HTTP routes. A route
// registered anywhere else fails TestGuardChainMatchesRegistration because the
// parser will not see it and the booted router will.
var routeRegistrationFiles = []string{
	"routes/",
}

// TestGuardChainMatchesRegistration fails when a served route has no row, a
// row is not served, or the row's chain is not the middleware the route files
// register. Cors and CacheControl are omitted. The check does not use the
// architecture baseline: a mismatch is always a failure.
func TestGuard_Chain_MatchesRegistration(t *testing.T) {
	module, err := architecture.LoadModule()
	if err != nil {
		t.Fatalf("load module: %v", err)
	}
	registered, err := registeredGuardChains(module)
	if err != nil {
		t.Fatalf("read route registration: %v", err)
	}
	for route, entry := range routeTable {
		got, ok := registered[route]
		if !ok {
			t.Errorf("%s is in routeTable but the route files do not register it", route)
			continue
		}
		if got != entry.chain {
			t.Errorf("%s chain = %q, route files register %q", route, entry.chain, got)
		}
	}
	for route := range registered {
		if _, listed := routeTable[route]; !listed {
			t.Errorf("%s is registered but has no row in routeTable", route)
		}
	}
	served := servedRoutes()
	for route := range served {
		if _, listed := routeTable[route]; !listed {
			t.Errorf("%s is served but has no row in routeTable", route)
		}
		if _, parsed := registered[route]; !parsed {
			t.Errorf("%s is served but the registration parser missed it", route)
		}
	}
	for route := range routeTable {
		if !served[route] {
			t.Errorf("%s has a row in routeTable but is not served", route)
		}
	}
}

type routeFrame struct {
	prefix string
	guards []string
}

func registeredGuardChains(module *architecture.Module) (map[string]string, error) {
	perms := permissionConstants(module)
	var global []string
	for _, file := range registrationFiles(module) {
		collectGlobalGuards(file.AST, perms, &global)
	}
	registered := map[string]string{}
	for _, file := range registrationFiles(module) {
		if err := collectRoutes(file.AST, freshFrame(global), perms, registered); err != nil {
			return nil, err
		}
	}
	return registered, nil
}

func registrationFiles(module *architecture.Module) []*architecture.SourceFile {
	var files []*architecture.SourceFile
	for _, file := range module.ProductionFiles() {
		for _, prefix := range routeRegistrationFiles {
			if file.Path == prefix || strings.HasPrefix(file.Path, prefix) {
				files = append(files, file)
				break
			}
		}
	}
	return files
}

func permissionConstants(module *architecture.Module) map[string]string {
	decls := map[string]map[string]ast.Expr{}
	for _, dir := range []string{"app/policies", "app/models"} {
		for _, file := range module.ProductionFiles(dir) {
			pkg := file.AST.Name.Name
			if decls[pkg] == nil {
				decls[pkg] = map[string]ast.Expr{}
			}
			for _, decl := range file.AST.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.CONST {
					continue
				}
				for _, spec := range gen.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok || len(value.Names) != 1 || len(value.Values) != 1 {
						continue
					}
					decls[pkg][value.Names[0].Name] = value.Values[0]
				}
			}
		}
	}
	names := map[string]string{}
	for name := range decls["policies"] {
		text, ok := resolvePermissionConst(decls, "policies", name, map[string]bool{})
		if ok {
			names[name] = text
		}
	}
	return names
}

func resolvePermissionConst(decls map[string]map[string]ast.Expr, pkg, name string, seen map[string]bool) (string, bool) {
	key := pkg + "." + name
	if seen[key] {
		return "", false
	}
	seen[key] = true
	pkgDecls := decls[pkg]
	if pkgDecls == nil {
		return "", false
	}
	expr, ok := pkgDecls[name]
	if !ok {
		return "", false
	}
	switch typed := expr.(type) {
	case *ast.BasicLit:
		if typed.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(typed.Value)
		if err != nil {
			return "", false
		}
		return text, true
	case *ast.Ident:
		return resolvePermissionConst(decls, pkg, typed.Name, seen)
	case *ast.SelectorExpr:
		pkgIdent, ok := typed.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		return resolvePermissionConst(decls, pkgIdent.Name, typed.Sel.Name, seen)
	default:
		return "", false
	}
}

func collectGlobalGuards(file *ast.File, perms map[string]string, global *[]string) {
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "GlobalMiddleware" {
			return true
		}
		*global = append(*global, guardLabels(call.Args, nil, perms)...)
		return true
	})
}

func collectRoutes(file *ast.File, global routeFrame, perms map[string]string, registered map[string]string) error {
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		if err := walkBlock(function.Body, global, global.guards, map[string]string{}, perms, registered); err != nil {
			return err
		}
	}
	return nil
}

func walkBlock(block *ast.BlockStmt, frame routeFrame, global []string, bindings map[string]string, perms map[string]string, registered map[string]string) error {
	if block == nil {
		return nil
	}
	for _, statement := range block.List {
		switch typed := statement.(type) {
		case *ast.AssignStmt:
			bindGuard(typed, bindings, perms)
		case *ast.ExprStmt:
			if err := walkRouteCall(typed.X, frame, global, bindings, perms, registered); err != nil {
				return err
			}
		case *ast.IfStmt:
			if err := walkBlock(typed.Body, frame, global, bindings, perms, registered); err != nil {
				return err
			}
			elseBlock, _ := typed.Else.(*ast.BlockStmt)
			if err := walkBlock(elseBlock, frame, global, bindings, perms, registered); err != nil {
				return err
			}
		case *ast.ForStmt:
			if err := walkBlock(typed.Body, frame, global, bindings, perms, registered); err != nil {
				return err
			}
		case *ast.RangeStmt:
			if err := walkBlock(typed.Body, frame, global, bindings, perms, registered); err != nil {
				return err
			}
		case *ast.SwitchStmt:
			if err := walkSwitch(typed, frame, global, bindings, perms, registered); err != nil {
				return err
			}
		}
	}
	return nil
}

func walkSwitch(statement *ast.SwitchStmt, frame routeFrame, global []string, bindings map[string]string, perms map[string]string, registered map[string]string) error {
	if statement.Body == nil {
		return nil
	}
	for _, statement := range statement.Body.List {
		clause, ok := statement.(*ast.CaseClause)
		if !ok {
			continue
		}
		if err := walkBlock(&ast.BlockStmt{List: clause.Body}, frame, global, bindings, perms, registered); err != nil {
			return err
		}
	}
	return nil
}

func bindGuard(assign *ast.AssignStmt, bindings map[string]string, perms map[string]string) {
	if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return
	}
	name, ok := assign.Lhs[0].(*ast.Ident)
	if !ok {
		return
	}
	label, ok := guardLabel(assign.Rhs[0], bindings, perms)
	if !ok {
		return
	}
	bindings[name.Name] = label
}

func walkRouteCall(expr ast.Expr, frame routeFrame, global []string, bindings map[string]string, perms map[string]string, registered map[string]string) error {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	switch selector.Sel.Name {
	case "Group":
		next := frameFromReceiver(selector.X, frame, global, bindings, perms)
		function, ok := call.Args[0].(*ast.FuncLit)
		if !ok || function.Body == nil {
			return nil
		}
		return walkBlock(function.Body, next, global, cloneBindings(bindings), perms, registered)
	case "Get", "Post", "Put", "Patch", "Delete", "Head", "Options":
		next := frameFromReceiver(selector.X, frame, global, bindings, perms)
		if len(call.Args) == 0 {
			return nil
		}
		path, ok := stringLiteral(call.Args[0])
		if !ok {
			return nil
		}
		key := routeKey(selector.Sel.Name, joinPath(next.prefix, path))
		chain := strings.Join(next.guards, " > ")
		if previous, exists := registered[key]; exists && previous != chain {
			registered[key] = previous + " CONFLICT " + chain
			return nil
		}
		registered[key] = chain
	}
	return nil
}

func frameFromReceiver(recv ast.Expr, frame routeFrame, global []string, bindings map[string]string, perms map[string]string) routeFrame {
	call, ok := recv.(*ast.CallExpr)
	if !ok {
		return frame
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return frame
	}
	switch selector.Sel.Name {
	case "Route":
		return freshFrame(global)
	case "Prefix":
		frame = frameFromReceiver(selector.X, frame, global, bindings, perms)
		if len(call.Args) == 0 {
			return frame
		}
		path, ok := stringLiteral(call.Args[0])
		if !ok {
			frame.prefix = joinPath(frame.prefix, "?")
			return frame
		}
		frame.prefix = joinPath(frame.prefix, path)
		return frame
	case "Middleware":
		frame = frameFromReceiver(selector.X, frame, global, bindings, perms)
		return withGuards(frame, guardLabels(call.Args, bindings, perms))
	default:
		return frameFromReceiver(selector.X, frame, global, bindings, perms)
	}
}

// freshFrame starts a router from the global guards. The slice is copied so a
// later Middleware does not mutate the guards of another route. Route() drops
// any prefix and any group guards and keeps only these globals.
func freshFrame(global []string) routeFrame {
	guards := make([]string, len(global))
	copy(guards, global)
	return routeFrame{guards: guards}
}

func withGuards(frame routeFrame, extra []string) routeFrame {
	guards := make([]string, 0, len(frame.guards)+len(extra))
	guards = append(guards, frame.guards...)
	guards = append(guards, extra...)
	frame.guards = guards
	return frame
}

func guardLabels(args []ast.Expr, bindings map[string]string, perms map[string]string) []string {
	labels := make([]string, 0, len(args))
	for _, arg := range args {
		label, ok := guardLabel(arg, bindings, perms)
		if !ok || label == "" {
			continue
		}
		labels = append(labels, label)
	}
	return labels
}

func guardLabel(expr ast.Expr, bindings map[string]string, perms map[string]string) (string, bool) {
	switch typed := expr.(type) {
	case *ast.Ident:
		label, ok := bindings[typed.Name]
		return label, ok
	case *ast.CallExpr:
		selector, ok := typed.Fun.(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != "middleware" {
			return "", false
		}
		if skippedGuards[selector.Sel.Name] {
			return "", true
		}
		if selector.Sel.Name == "APIScope" || selector.Sel.Name == "Can" || selector.Sel.Name == "WalletCan" {
			if len(typed.Args) != 1 {
				return selector.Sel.Name + "(?)", true
			}
			return selector.Sel.Name + "(" + permissionArg(typed.Args[0], perms) + ")", true
		}
		return selector.Sel.Name, true
	default:
		return "", false
	}
}

func permissionArg(expr ast.Expr, perms map[string]string) string {
	switch typed := expr.(type) {
	case *ast.BasicLit:
		text, err := strconv.Unquote(typed.Value)
		if err != nil {
			return "?"
		}
		return text
	case *ast.SelectorExpr:
		if text, ok := perms[typed.Sel.Name]; ok {
			return text
		}
		return "?"
	default:
		return "?"
	}
}

func stringLiteral(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	text, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return text, true
}

func routeKey(method, path string) string {
	if method == "Get" {
		return "GET|HEAD " + path
	}
	return strings.ToUpper(method) + " " + path
}

func joinPath(prefix, path string) string {
	if path == "" || path == "/" {
		if prefix == "" {
			return "/"
		}
		return prefix
	}
	if prefix == "" || prefix == "/" {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return path
	}
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(path, "/")
}

func cloneBindings(bindings map[string]string) map[string]string {
	copied := make(map[string]string, len(bindings))
	for name, label := range bindings {
		copied[name] = label
	}
	return copied
}
