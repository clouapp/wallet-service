package architecture

import (
	"reflect"
	"strings"
	"testing"
)

// persistenceImports are what the HTTP layer may never import.
var persistenceImports = map[string]bool{
	"gorm.io/gorm":            true,
	"database/sql":            true,
	"github.com/jmoiron/sqlx": true,
	"github.com/goravel/framework/database/orm": true,
}

// TestHTTPLayerDoesNotReachPersistence reports app/http code that imports a
// repository or a database library, or calls facades.Orm() / facades.DB() /
// facades.App().
func TestHTTP_Layer_DoesNotReachPersistence(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.ProductionFiles("app/http") {
		for _, importPath := range file.Imports() {
			target, inModule := module.RelativeOf(importPath)
			if (inModule && Layer(target) == LayerRepositories) || persistenceImports[importPath] {
				violations.Add("%s imports %s", file.Path, importPath)
			}
		}
		calls := packageCalls(file, goravelFacades, "facades")
		for _, facade := range []string{"Orm", "DB", "App"} {
			for range calls[facade] {
				violations.Add("%s calls facades.%s()", file.Path, facade)
			}
		}
	}
	Report(t, &violations)
}

// TestControllersTakeTheirDependenciesByConstructor reports controllers that
// look their dependencies up through the container instead of receiving them.
func TestControllers_Take_TheirDependenciesByConstructor(t *testing.T) {
	module := sharedModule(t)
	containerPath := module.ImportPathOf("app/container")
	var violations Violations
	for _, file := range module.ProductionFiles("app/http/controllers") {
		for function, count := range packageCalls(file, containerPath, "container") {
			for range count {
				violations.Add("%s calls container.%s()", file.Path, function)
			}
		}
	}
	Report(t, &violations)
}

// requestReaders are the ctx.Request() methods a handler may not call: the
// input goes through a form request (requests.Validate / ValidatePath / RouteID).
var requestReaders = map[string]bool{
	"All": true, "Bind": true, "Input": true, "InputArray": true, "InputBool": true,
	"InputInt": true, "InputInt64": true, "InputMap": true, "Json": true, "Query": true,
	"QueryArray": true, "QueryBool": true, "QueryInt": true, "QueryInt64": true,
	"QueryMap": true, "Queries": true, "Route": true, "RouteInt": true,
	"RouteInt64": true, "Validate": true, "ValidateRequest": true, "File": true,
}

// TestHandlersReadTheirInputThroughAFormRequest reports controllers reading
// the request directly.
func TestHandlers_Read_TheirInputThroughAFormRequest(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.ProductionFiles("app/http/controllers") {
		for method, count := range chainedCalls(file, "Request") {
			if !requestReaders[method] {
				continue
			}
			for range count {
				violations.Add("%s calls ctx.Request().%s()", file.Path, method)
			}
		}
	}
	Report(t, &violations)
}

// TestControllersDoNotWriteJSONInline reports bodies written with
// ctx.Response().Json() in controllers and middleware. The target is one
// writer (responses.JSON) rendering resources; which shape it writes is
// decision B2.1/B2.3 of the alignment plan, so this check only counts the
// inline writes (the measurable part of Test2xxResponsesAreResources).
func TestControllers_Do_NotWriteJSONInline(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.ProductionFiles("app/http/controllers", "app/http/middleware") {
		for range chainedCalls(file, "Response")["Json"] {
			violations.Add("%s calls ctx.Response().Json()", file.Path)
		}
	}
	Report(t, &violations)
}

// TestEveryFormRequestFieldTagsFormAndJSONAlike reports a form request field
// whose form tag is missing or differs from its json tag: the binder reads
// form, so a mismatch binds nothing, silently.
func TestEvery_Form_RequestFieldTagsFormAndJSONAlike(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.ProductionFiles("app/http/requests") {
		structTags(file, func(typeName, fieldName, tag string) {
			jsonName := tagName(tag, "json")
			formName := tagName(tag, "form")
			if jsonName == "" || jsonName == "-" {
				return
			}
			if formName != jsonName {
				violations.Add("%s: %s.%s has json %q but form %q", file.Path, typeName, fieldName, jsonName, formName)
			}
		})
	}
	Report(t, &violations)
}

// TestContextValuesAreReadOnlyInMiddleware reports string-keyed context reads
// and writes outside app/http/middleware: the actor and the scope are read
// through typed accessors (.ai/guidelines/identity-and-scope.md).
func TestContext_Values_AreReadOnlyInMiddleware(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.ProductionFiles("app") {
		if hasAnyPrefix(file.Dir, []string{"app/http/middleware"}) {
			continue
		}
		for method, count := range stringKeyedCalls(file, "Value", "WithValue") {
			for range count {
				violations.Add("%s calls ctx.%s(\"...\")", file.Path, method)
			}
		}
	}
	Report(t, &violations)
}

// tagName returns the name part of one key of a struct tag.
func tagName(tag, key string) string {
	value, ok := reflect.StructTag(tag).Lookup(key)
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(value, ",")
	return name
}
