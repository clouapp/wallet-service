package architecture

import (
	"go/ast"
	"go/token"
	"strconv"
	"sync"
	"testing"
)

const goravelFacades = "github.com/goravel/framework/facades"

var (
	loadOnce     sync.Once
	loadedModule *Module
	errLoad      error
)

// sharedModule parses the repository once for every check.
func sharedModule(t *testing.T) *Module {
	t.Helper()
	loadOnce.Do(func() { loadedModule, errLoad = LoadModule() })
	if errLoad != nil {
		t.Fatalf("load module: %v", errLoad)
	}
	return loadedModule
}

// localName returns the identifier a file uses for an import path, or "" when
// the file does not import it (blank and dot imports count as absent).
func localName(file *SourceFile, importPath, defaultName string) string {
	for _, spec := range file.AST.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		if spec.Name == nil {
			return defaultName
		}
		if spec.Name.Name == "_" || spec.Name.Name == "." {
			return ""
		}
		return spec.Name.Name
	}
	return ""
}

// packageCalls counts calls of the form pkg.Function(...) per function name,
// pkg being the local name of importPath in the file.
func packageCalls(file *SourceFile, importPath, defaultName string) map[string]int {
	calls := map[string]int{}
	name := localName(file, importPath, defaultName)
	if name == "" {
		return calls
	}
	ast.Inspect(file.AST, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := selector.X.(*ast.Ident); ok && ident.Name == name {
			calls[selector.Sel.Name]++
		}
		return true
	})
	return calls
}

// chainedCalls counts calls of the form <x>.<outer>().<method>(...) per method,
// e.g. ctx.Request().Input(...) with outer "Request".
func chainedCalls(file *SourceFile, outer string) map[string]int {
	calls := map[string]int{}
	ast.Inspect(file.AST, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		inner, ok := selector.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		innerSelector, ok := inner.Fun.(*ast.SelectorExpr)
		if ok && innerSelector.Sel.Name == outer && len(inner.Args) == 0 {
			calls[selector.Sel.Name]++
		}
		return true
	})
	return calls
}

// stringKeyedCalls counts calls of <x>.<method>("literal", ...) per method.
func stringKeyedCalls(file *SourceFile, methods ...string) map[string]int {
	wanted := map[string]bool{}
	for _, method := range methods {
		wanted[method] = true
	}
	calls := map[string]int{}
	ast.Inspect(file.AST, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !wanted[selector.Sel.Name] {
			return true
		}
		if literal, ok := call.Args[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
			calls[selector.Sel.Name]++
		}
		return true
	})
	return calls
}

// structTags visits every field tag of every struct type declared in the file.
func structTags(file *SourceFile, visit func(typeName, fieldName, tag string)) {
	ast.Inspect(file.AST, func(node ast.Node) bool {
		spec, ok := node.(*ast.TypeSpec)
		if !ok {
			return true
		}
		structType, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, field := range structType.Fields.List {
			if field.Tag == nil {
				continue
			}
			tag, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				continue
			}
			fieldName := "(embedded)"
			if len(field.Names) > 0 {
				fieldName = field.Names[0].Name
			}
			visit(spec.Name.Name, fieldName, tag)
		}
		return true
	})
}
