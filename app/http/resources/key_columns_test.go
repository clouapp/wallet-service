package resources

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// keyColumnStems are the normalized names of key columns. A resource field
// matches when its Go name or json name contains one of them: share, private
// key, seed, passphrase, encrypted_user_key, encrypted_passcode, and the key
// columns the custody guideline names (customer share, share IV and salt,
// secret ARN, hdkey, chain code), plus the address private-key envelope.
// AddressIndex is the next derivation counter, not a key column.
var keyColumnStems = []string{
	"share",
	"privatekey",
	"seed",
	"passphrase",
	"encrypteduserkey",
	"encryptedpasscode",
	"hdkey",
	"chaincode",
	"secretarn",
	"encryptioniv",
	"encryptionsalt",
}

// TestNoResourceTypeHasAFieldNamedLikeAKeyColumn walks every HTTP resource
// struct and fails when a field is named like a key column. The field is
// absent from the struct; a json:"-" tag is not a substitute.
func TestNoResourceTypeHasAFieldNamedLikeAKeyColumn(t *testing.T) {
	for _, name := range []string{
		"share", "ShareA", "share_b", "MPCCustomerShare", "MPCShareIV", "MPCShareSalt",
		"private_key", "PrivateKey", "EncryptedPrivateKey",
		"seed", "SeedHex", "passphrase", "Passphrase",
		"encrypted_user_key", "EncryptedUserKey", "encrypted_passcode", "EncryptedPasscode",
		"hdkey", "HDKey", "MPCChainCode", "chain_code", "MPCSecretARN",
		"EncryptionIV", "EncryptionSalt",
	} {
		if !namedLikeKeyColumn(name) {
			t.Errorf("namedLikeKeyColumn(%q) = false, want true", name)
		}
	}
	for _, name := range []string{
		"AddressIndex", "address_index", "DerivationIndex", "AssetKey", "IdempotencyKey",
		"GasStatus", "gas_status", "PasswordHash", "TotpSecret", "ServicePublicKey",
		"ActivationCode", "RequiredApprovals",
	} {
		if namedLikeKeyColumn(name) {
			t.Errorf("namedLikeKeyColumn(%q) = true, want false", name)
		}
	}

	root := resourceRoot(t)
	fset := token.NewFileSet()
	var structs int
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Errorf("parse %s: %v", path, err)
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					continue
				}
				structs++
				for _, field := range st.Fields.List {
					for _, name := range resourceFieldNames(field) {
						if namedLikeKeyColumn(name) {
							t.Errorf("%s: %s.%s is named like a key column", rel, ts.Name.Name, name)
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if structs == 0 {
		t.Fatal("no resource structs found")
	}
}

func resourceRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate resource test")
	}
	return filepath.Dir(file)
}

func resourceFieldNames(field *ast.Field) []string {
	var names []string
	if len(field.Names) == 0 {
		if name := typeIdent(field.Type); name != "" {
			names = append(names, name)
		}
	} else {
		for _, id := range field.Names {
			names = append(names, id.Name)
		}
	}
	if name := jsonFieldName(field); name != "" {
		names = append(names, name)
	}
	return names
}

func typeIdent(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	case *ast.StarExpr:
		return typeIdent(typed.X)
	default:
		return ""
	}
}

func jsonFieldName(field *ast.Field) string {
	if field.Tag == nil {
		return ""
	}
	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return ""
	}
	name := reflect.StructTag(raw).Get("json")
	if name == "" || name == "-" {
		return ""
	}
	return name
}

func namedLikeKeyColumn(name string) bool {
	normalized := normalizeKeyName(name)
	if normalized == "" {
		return false
	}
	for _, stem := range keyColumnStems {
		if strings.Contains(normalized, stem) {
			return true
		}
	}
	return false
}

func normalizeKeyName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r == '_' || r == '-' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
