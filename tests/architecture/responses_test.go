package architecture

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// Test2xxResponsesAreResources fails when a JSON body written by app/http
// encodes a model or a value that embeds one. Resources own the wire.
// Success and other statuses are both checked: a model must not leave as JSON.
func TestCode2_Responses_AreResources(t *testing.T) {
	module := sharedModule(t)
	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:  module.Root,
	}, "./app/http/...")
	if err != nil {
		t.Fatalf("load app/http: %v", err)
	}
	funcs := map[types.Object]*ast.FuncDecl{}
	infoOf := map[types.Object]*types.Info{}
	for _, pkg := range pkgs {
		for _, pkgErr := range pkg.Errors {
			t.Errorf("typecheck %s: %v", pkg.PkgPath, pkgErr)
		}
		if pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name == nil {
					continue
				}
				obj := pkg.TypesInfo.Defs[fn.Name]
				if obj == nil {
					continue
				}
				funcs[obj] = fn
				infoOf[obj] = pkg.TypesInfo
			}
		}
	}
	if t.Failed() {
		return
	}

	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil || !strings.HasPrefix(pkg.PkgPath, module.Path+"/app/http/") {
			continue
		}
		for _, file := range pkg.Syntax {
			filename := pkg.Fset.Position(file.Pos()).Filename
			if strings.HasSuffix(filename, "_test.go") {
				continue
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					body, kind := responseJSONBody(call)
					if body == nil {
						return true
					}
					w := &responseWalker{funcs: funcs, infoOf: infoOf, modulePath: module.Path, visiting: map[types.Object]bool{}}
					if model, found := w.encoded(pkg.TypesInfo, fn, body, 0); found {
						pos := pkg.Fset.Position(call.Pos())
						rel, relErr := filepath.Rel(module.Root, pos.Filename)
						if relErr != nil {
							rel = pos.Filename
						}
						t.Errorf("%s:%d %s encodes %s", filepath.ToSlash(rel), pos.Line, kind, model)
					}
					return true
				})
			}
		}
	}
}

func responseJSONBody(call *ast.CallExpr) (ast.Expr, string) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, ""
	}
	switch sel.Sel.Name {
	case "Json":
		if len(call.Args) == 1 {
			return call.Args[0], "Json"
		}
		if len(call.Args) >= 2 {
			return call.Args[len(call.Args)-1], "Json"
		}
	case "Send", "JSON":
		if len(call.Args) >= 3 {
			return call.Args[2], sel.Sel.Name
		}
	}
	return nil, ""
}

type responseWalker struct {
	funcs      map[types.Object]*ast.FuncDecl
	infoOf     map[types.Object]*types.Info
	modulePath string
	visiting   map[types.Object]bool
}

func (w *responseWalker) encoded(info *types.Info, fn *ast.FuncDecl, expr ast.Expr, depth int) (string, bool) {
	if expr == nil || depth > 16 {
		return "", false
	}
	if path, ok := responseTypeHasModel(w.modulePath, info.TypeOf(expr), map[types.Type]bool{}); ok {
		return path, true
	}
	switch typed := expr.(type) {
	case *ast.ParenExpr:
		return w.encoded(info, fn, typed.X, depth)
	case *ast.Ident:
		for _, origin := range responseOrigins(info, fn, typed) {
			if path, ok := w.encoded(info, fn, origin, depth+1); ok {
				return path, true
			}
		}
	case *ast.CompositeLit:
		for _, elt := range typed.Elts {
			value := elt
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				value = kv.Value
			}
			if path, ok := w.encoded(info, fn, value, depth+1); ok {
				return path, true
			}
		}
	case *ast.CallExpr:
		if fun, ok := typed.Fun.(*ast.Ident); ok && fun.Name == "append" {
			for _, arg := range typed.Args[1:] {
				if path, ok := w.encoded(info, fn, arg, depth+1); ok {
					return path, true
				}
			}
			if len(typed.Args) > 0 {
				return w.encoded(info, fn, typed.Args[0], depth+1)
			}
			return "", false
		}
		if !responseMapOrInterface(info.TypeOf(typed)) {
			return "", false
		}
		obj := responseCallee(info, typed)
		target := w.funcs[obj]
		if obj == nil || target == nil || target.Body == nil || w.visiting[obj] {
			return "", false
		}
		w.visiting[obj] = true
		defer delete(w.visiting, obj)
		calleeInfo := w.infoOf[obj]
		subs := responseSubstitutions(info, fn, calleeInfo, target, typed)
		var found string
		ast.Inspect(target.Body, func(node ast.Node) bool {
			if found != "" {
				return false
			}
			ret, ok := node.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			for _, result := range ret.Results {
				if path, ok := w.encodedWithSubs(calleeInfo, target, subs, result, depth+1); ok {
					found = path
					return false
				}
			}
			return true
		})
		return found, found != ""
	}
	return "", false
}

func (w *responseWalker) encodedWithSubs(info *types.Info, fn *ast.FuncDecl, subs map[*types.Var]responseSub, expr ast.Expr, depth int) (string, bool) {
	if ident, ok := expr.(*ast.Ident); ok {
		if param, ok := info.ObjectOf(ident).(*types.Var); ok {
			if replacement, ok := subs[param]; ok {
				return w.encoded(replacement.info, replacement.fn, replacement.expr, depth)
			}
		}
	}
	return w.encoded(info, fn, expr, depth)
}

type responseSub struct {
	info *types.Info
	fn   *ast.FuncDecl
	expr ast.Expr
}

func responseSubstitutions(caller *types.Info, callerFn *ast.FuncDecl, callee *types.Info, fn *ast.FuncDecl, call *ast.CallExpr) map[*types.Var]responseSub {
	out := map[*types.Var]responseSub{}
	if fn.Type == nil || fn.Type.Params == nil || callee == nil {
		return out
	}
	i := 0
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if i >= len(call.Args) {
				return out
			}
			obj, _ := callee.Defs[name].(*types.Var)
			if obj != nil && responseMapOrInterface(callee.TypeOf(name)) {
				out[obj] = responseSub{info: caller, fn: callerFn, expr: call.Args[i]}
			}
			i++
		}
	}
	return out
}

func responseOrigins(info *types.Info, fn *ast.FuncDecl, ident *ast.Ident) []ast.Expr {
	if fn == nil || fn.Body == nil || ident == nil {
		return nil
	}
	obj := info.ObjectOf(ident)
	if obj == nil {
		return nil
	}
	var found []ast.Expr
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.AssignStmt:
			for i, lhs := range typed.Lhs {
				if i >= len(typed.Rhs) {
					continue
				}
				if responseRefers(info, lhs, obj) {
					found = append(found, typed.Rhs[i])
				}
				if index, ok := lhs.(*ast.IndexExpr); ok && responseRefers(info, index.X, obj) {
					found = append(found, typed.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			for i, name := range typed.Names {
				if info.ObjectOf(name) != obj || i >= len(typed.Values) {
					continue
				}
				found = append(found, typed.Values[i])
			}
		}
		return true
	})
	return found
}

func responseRefers(info *types.Info, expr ast.Expr, obj types.Object) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && info.ObjectOf(ident) == obj
}

func responseCallee(info *types.Info, call *ast.CallExpr) types.Object {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return info.Uses[fun]
	case *ast.SelectorExpr:
		return info.Uses[fun.Sel]
	default:
		return nil
	}
}

func responseMapOrInterface(t types.Type) bool {
	if t == nil {
		return true
	}
	switch typed := t.(type) {
	case *types.Named:
		return responseMapOrInterface(typed.Underlying())
	case *types.Alias:
		return responseMapOrInterface(types.Unalias(t))
	case *types.Map, *types.Interface:
		return true
	default:
		return false
	}
}

func responseTypeHasModel(modulePath string, t types.Type, seen map[types.Type]bool) (string, bool) {
	if t == nil || seen[t] {
		return "", false
	}
	seen[t] = true
	switch typed := t.(type) {
	case *types.Pointer:
		return responseTypeHasModel(modulePath, typed.Elem(), seen)
	case *types.Slice:
		return responseTypeHasModel(modulePath, typed.Elem(), seen)
	case *types.Array:
		return responseTypeHasModel(modulePath, typed.Elem(), seen)
	case *types.Map:
		if path, ok := responseTypeHasModel(modulePath, typed.Key(), seen); ok {
			return path, true
		}
		return responseTypeHasModel(modulePath, typed.Elem(), seen)
	case *types.Named:
		if responseIsModelPackage(modulePath, typed.Obj().Pkg()) {
			return typed.Obj().Pkg().Path() + "." + typed.Obj().Name(), true
		}
		if typed.Obj().Pkg() != nil && strings.HasPrefix(typed.Obj().Pkg().Path(), modulePath+"/") {
			return responseTypeHasModel(modulePath, typed.Underlying(), seen)
		}
		return "", false
	case *types.Alias:
		if responseIsModelPackage(modulePath, typed.Obj().Pkg()) {
			return typed.Obj().Pkg().Path() + "." + typed.Obj().Name(), true
		}
		return responseTypeHasModel(modulePath, types.Unalias(typed), seen)
	case *types.Struct:
		for i := 0; i < typed.NumFields(); i++ {
			field := typed.Field(i)
			if path, ok := responseTypeHasModel(modulePath, field.Type(), seen); ok {
				name := field.Name()
				if field.Embedded() {
					name = "(embedded) " + name
				}
				return name + " " + path, true
			}
		}
		return "", false
	default:
		return "", false
	}
}

func responseIsModelPackage(modulePath string, pkg *types.Package) bool {
	if pkg == nil {
		return false
	}
	return pkg.Path() == modulePath+"/app/models"
}

// TestError_Bodies_GoThroughTheResponsesWriters reports a map literal with an
// "error" key whose value is not an object, in production code under app/ or
// routes/: the legacy {"error":"text"} shape. responses.Send no longer wraps
// it into the envelope, so it would reach the wire as written. A failure is
// written by responses.Fail, FailWith, FailMessage or Error
// (.ai/guidelines/http-error-contract.md).
func TestError_Bodies_GoThroughTheResponsesWriters(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.ProductionFiles("app", "routes") {
		ast.Inspect(file.AST, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || !isMapLiteral(literal) {
				return true
			}
			for _, element := range literal.Elts {
				pair, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := pair.Key.(*ast.BasicLit)
				if !ok || key.Kind != token.STRING {
					continue
				}
				if name, err := strconv.Unquote(key.Value); err != nil || name != "error" {
					continue
				}
				if _, object := pair.Value.(*ast.CompositeLit); object {
					continue
				}
				violations.Add("%s builds a legacy {\"error\": ...} map", file.Path)
			}
			return true
		})
	}
	Report(t, &violations)
}

// isMapLiteral reports a map composite literal: map[...]..., or a named map
// such as http.Json.
func isMapLiteral(literal *ast.CompositeLit) bool {
	switch typed := literal.Type.(type) {
	case *ast.MapType:
		return true
	case *ast.SelectorExpr:
		return typed.Sel.Name == "Json"
	case *ast.Ident:
		return typed.Name == "Json"
	default:
		return false
	}
}
