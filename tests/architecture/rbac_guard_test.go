package architecture

import (
	"go/ast"
	"go/token"
	"sort"
	"strconv"
	"testing"
)

// rbacPivotTables are the platform RBAC pivots. Only app/policies may read
// them. Creating the tables in a migration is not a read.
var rbacPivotTables = map[string]bool{
	"model_has_roles":       true,
	"role_has_permissions":  true,
	"model_has_permissions": true,
}

// rbacPivotWriters may name a pivot table while creating or seeding it.
var rbacPivotWriters = []string{
	"app/policies",
	"database/migrations",
	"database/seeders",
}

// TestOnlyPoliciesCallAccountRoleOutranks fails when any production file
// outside app/policies calls models.AccountRoleOutranks. The function is the
// rank comparison; defining it in app/models is not a call.
func TestOnly_Policies_CallAccountRoleOutranks(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.ProductionFiles() {
		if hasAnyPrefix(file.Dir, []string{"app/policies"}) {
			continue
		}
		if callsAccountRoleOutranks(file.AST) {
			violations.Add("%s calls AccountRoleOutranks", file.Path)
		}
	}
	Report(t, &violations)
}

// TestOnlyPoliciesReadRBACPivots fails when any production file outside
// app/policies and the schema writers names a pivot table.
func TestOnly_Policies_ReadRBACPivots(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.ProductionFiles() {
		if hasAnyPrefix(file.Dir, rbacPivotWriters) {
			continue
		}
		for _, name := range pivotTablesNamed(file.AST) {
			violations.Add("%s reads RBAC pivot %s", file.Path, name)
		}
	}
	Report(t, &violations)
}

func callsAccountRoleOutranks(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch function := call.Fun.(type) {
		case *ast.Ident:
			if function.Name == "AccountRoleOutranks" {
				found = true
			}
		case *ast.SelectorExpr:
			if function.Sel.Name == "AccountRoleOutranks" {
				found = true
			}
		}
		return true
	})
	return found
}

func pivotTablesNamed(file *ast.File) []string {
	found := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(literal.Value)
		if err != nil || !rbacPivotTables[text] {
			return true
		}
		found[text] = true
		return true
	})
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
