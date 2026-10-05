package architecture

import (
	"go/ast"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// TestModelsCarryNoWireTags fails when a model field carries an HTTP json
// name. The wire shape lives only in app/http/resources. json:"-" stays on
// fields that must not become visible. UserPreferences keeps the two names
// of the users.preferences jsonb document.
func TestModelsCarryNoWireTags(t *testing.T) {
	module := sharedModule(t)
	for _, file := range module.ProductionFiles("app/models", "pkg/authmodel") {
		structTags(file, func(typeName, fieldName, tag string) {
			value, ok := reflect.StructTag(tag).Lookup("json")
			if !ok {
				return
			}
			name, _, _ := strings.Cut(value, ",")
			if name == "-" {
				return
			}
			if file.Path == "pkg/authmodel/preferences.go" && typeName == "UserPreferences" &&
				(name == "preferred_fiat_code" || name == "display_in_fiat") {
				return
			}
			t.Errorf("%s: %s.%s has json tag %q", file.Path, typeName, fieldName, value)
		})
	}
}

// ioLibraries are I/O clients a service reaches through a port implemented in
// app/adapters, never directly (.ai/guidelines/controllers-and-services.md).
var ioLibraries = []string{
	"net/http",
	"database/sql",
	"gorm.io/",
	"github.com/redis/go-redis",
	"github.com/aws/aws-sdk-go-v2",
	"github.com/ethereum/go-ethereum/ethclient",
	"github.com/ethereum/go-ethereum/rpc",
	"github.com/btcsuite/btcd/rpcclient",
	"github.com/gagliardetto/solana-go/rpc",
	"github.com/gorilla/websocket",
	"github.com/goravel/framework/contracts/http",
}

// serviceFacades are facades a service receives through its Deps instead.
var serviceFacades = []string{"Orm", "DB", "Event", "Config", "Crypt", "Queue", "Mail", "Cache", "App", "Gate", "Auth"}

// TestServicesDoNotKnowTheHTTPLayer reports a service importing the HTTP
// layer, a repository, an adapter or an I/O library, or calling a facade it
// should receive through its Deps.
func TestServicesDoNotKnowTheHTTPLayer(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.ProductionFiles("app/services") {
		for _, importPath := range file.Imports() {
			if target, inModule := module.RelativeOf(importPath); inModule {
				switch Layer(target) {
				case LayerHTTP, LayerRepositories, LayerAdapters, LayerContainer:
					violations.Add("%s imports %s", file.Dir, target)
				}
				continue
			}
			for _, library := range ioLibraries {
				if importPath == library || strings.HasPrefix(importPath, strings.TrimSuffix(library, "/")+"/") {
					violations.Add("%s imports %s", file.Dir, importPath)
					break
				}
			}
		}
		calls := packageCalls(file, goravelFacades, "facades")
		for _, facade := range serviceFacades {
			for range calls[facade] {
				violations.Add("%s calls facades.%s()", file.Path, facade)
			}
		}
	}
	Report(t, &violations)
}

// roleNames are the account/wallet role values of both vocabularies in use
// (alignment plan S7).
var roleNames = map[string]bool{"owner": true, "admin": true, "auditor": true, "user": true, "viewer": true}

// TestOnlyPoliciesRankRoles reports a role compared outside app/policies.
func TestOnlyPoliciesRankRoles(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.ProductionFiles("app", "routes") {
		if hasAnyPrefix(file.Dir, []string{"app/policies"}) {
			continue
		}
		ast.Inspect(file.AST, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.BinaryExpr:
				if (typed.Op == token.EQL || typed.Op == token.NEQ) && (isRoleLiteral(typed.X) || isRoleLiteral(typed.Y)) {
					violations.Add("%s compares a role", file.Path)
				}
			case *ast.CaseClause:
				for _, expression := range typed.List {
					if isRoleLiteral(expression) {
						violations.Add("%s switches on a role", file.Path)
						break
					}
				}
			}
			return true
		})
	}
	Report(t, &violations)
}

// TestPermissionDecisionsGoThroughThePolicy reports a Gate asked outside
// app/policies, app/providers and app/http/middleware, and the in-handler
// authorize helper (.ai/guidelines/authorization.md).
func TestPermissionDecisionsGoThroughThePolicy(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.ProductionFiles("app", "routes") {
		if hasAnyPrefix(file.Dir, []string{"app/policies", "app/providers", "app/http/middleware"}) {
			continue
		}
		for range packageCalls(file, goravelFacades, "facades")["Gate"] {
			violations.Add("%s calls facades.Gate()", file.Path)
		}
		ast.Inspect(file.AST, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "authorize" {
				violations.Add("%s calls authorize()", file.Path)
			}
			return true
		})
	}
	Report(t, &violations)
}

// TestNoServiceLocatorOutsideTheCompositionRoot reports container.Get() and
// the string container key outside app/container, app/providers and main.go:
// the "god struct" must not come back once a package stops using it.
func TestNoServiceLocatorOutsideTheCompositionRoot(t *testing.T) {
	module := sharedModule(t)
	containerPath := module.ImportPathOf("app/container")
	var violations Violations
	for _, file := range module.ProductionFiles() {
		if hasAnyPrefix(file.Dir, []string{"app/container", "app/providers", "tests", "tools", "scripts"}) {
			continue
		}
		for range packageCalls(file, containerPath, "container")["Get"] {
			violations.Add("%s calls container.Get()", file.Path)
		}
		ast.Inspect(file.AST, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			if value, err := strconv.Unquote(literal.Value); err == nil && value == "vault.container" {
				violations.Add("%s uses the string container key", file.Path)
			}
			return true
		})
	}
	Report(t, &violations)
}

// TestFrameworkFacadesComeThroughAppFacades reports production packages that
// import github.com/goravel/framework/facades directly instead of a local
// app/facades (the xip rule). One entry per package, with its file count.
func TestFrameworkFacadesComeThroughAppFacades(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.ProductionFiles() {
		if hasAnyPrefix(file.Dir, []string{"app/facades", "tests", "tools", "scripts"}) {
			continue
		}
		if localName(file, goravelFacades, "facades") != "" {
			violations.Add("%s imports the framework facades", file.Dir)
		}
	}
	Report(t, &violations)
}

func isRoleLiteral(expression ast.Expr) bool {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(literal.Value)
	return err == nil && roleNames[value]
}
