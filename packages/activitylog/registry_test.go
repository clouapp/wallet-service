package activitylog

import (
	"testing"
)

func freshRegistry(t *testing.T) {
	t.Helper()
	resetRegistry()
	t.Cleanup(resetRegistry)
}

func TestRegisterAndLookup(t *testing.T) {
	freshRegistry(t)

	if err := Register(Table{Name: "publishers", LogName: "admin", Subject: "publisher", Columns: []string{"name"}}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := lookup("publishers")
	if !ok {
		t.Fatal("expected the table to be registered")
	}
	if got.Subject != "publisher" {
		t.Fatalf("subject: %q", got.Subject)
	}
	if _, ok := lookup("no-such-table"); ok {
		t.Fatal("an unregistered table must not answer ok=true")
	}
}

func TestRegisterRefusesAnUnnamedTable(t *testing.T) {
	freshRegistry(t)

	if err := Register(Table{Subject: "publisher"}); err == nil {
		t.Fatal("expected an error for a table with no name")
	}
}

// Registering the trail itself would make every entry produce an entry. The
// plugin also refuses it at capture time, independent of the registry — two
// locks on the same door, because a recursion here is not a wrong row, it is a
// process that never returns. See the spec §5.4.
func TestRegisterRefusesTheTrailItself(t *testing.T) {
	freshRegistry(t)

	if err := Register(Table{Name: trailTable, Columns: []string{"event"}}); err == nil {
		t.Fatal("expected an error when registering activity_log itself")
	}
}

func TestRegisterRefusesADuplicate(t *testing.T) {
	freshRegistry(t)

	if err := Register(Table{Name: "publishers", Columns: []string{"name"}}); err != nil {
		t.Fatalf("primeiro Register: %v", err)
	}
	if err := Register(Table{Name: "publishers", Columns: []string{"slug"}}); err == nil {
		t.Fatal("esperava erro no registro duplicado: um segundo registro silencioso trocaria a allowlist")
	}
}

// The allowlist is the redaction. A column absent from it is absent from the
// image even when the row carries it — which is the case for every write to
// users, because UserRepository.Update writes all eleven columns every time.
func TestImageKeepsOnlyAllowlistedColumns(t *testing.T) {
	tbl := Table{Name: "users", Columns: []string{"email", "status"}}

	img, flags := tbl.image(map[string]any{
		"id":          int64(42),
		"email":       "ana@acme.example",
		"status":      "active",
		"password":    "$2y$10$hash",
		"totp_secret": "sealed:abc",
	})

	if len(flags) != 0 {
		t.Fatalf("expected no flags, got %#v", flags)
	}
	if _, leaked := img["password"]; leaked {
		t.Fatal("password vazou para a imagem")
	}
	if _, leaked := img["totp_secret"]; leaked {
		t.Fatal("totp_secret vazou para a imagem")
	}
	if img["email"] != "ana@acme.example" || img["status"] != "active" {
		t.Fatalf("imagem perdeu coluna permitida: %#v", img)
	}
}

// A known category whose secret list is empty keeps its whole document.
// Reading "empty list" as "unrecognised" would prune categories whose diff
// is most worth having.
func TestImageKeepsTheDocumentForAKnownCategoryWithNoSecrets(t *testing.T) {
	tbl := Table{
		Name:    "settings",
		Columns: []string{"category", "payload"},
		JSONPaths: map[string]JSONRule{
			"payload": {RedactFor: func(map[string]any) ([]string, bool) { return nil, true }},
		},
	}

	img, flags := tbl.image(map[string]any{
		"category": "branding",
		"payload":  `{"logoUrl":"https://cdn.example/logo.png"}`,
	})

	if len(flags) != 0 {
		t.Fatalf("expected no flags, got %#v", flags)
	}
	doc, ok := img["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload devia ser um documento, veio %#v", img["payload"])
	}
	if _, kept := doc["logoUrl"]; !kept {
		t.Fatalf("a known category document with no secret was pruned: %#v", doc)
	}
}

func TestImageReplacesSecretPathsWithABoolean(t *testing.T) {
	tbl := Table{
		Name:    "settings",
		Columns: []string{"category", "payload"},
		JSONPaths: map[string]JSONRule{
			"payload": {RedactFor: func(map[string]any) ([]string, bool) {
				return []string{"authToken"}, true
			}},
		},
	}

	img, _ := tbl.image(map[string]any{
		"category": "unknown",
		"payload":  `{"backendUrl":"https://publisher.example","authToken":"sealed:xyz"}`,
	})

	doc := img["payload"].(map[string]any)
	if _, leaked := doc["authToken"]; leaked {
		t.Fatal("authToken vazou para a imagem")
	}
	if doc["authTokenSet"] != true {
		t.Fatalf("esperava authTokenSet=true, veio %#v", doc["authTokenSet"])
	}
	if doc["backendUrl"] != "https://publisher.example" {
		t.Fatalf("a non-secret field was lost: %#v", doc)
	}
}

// A category the redactor has no case for prunes EVERYTHING and says so. A new
// settings category is exactly the change that would otherwise leak on the day
// it ships: the category is added, the redactor has no branch, and nothing
// fails.
func TestImagePrunesEverythingForAnUnknownCategory(t *testing.T) {
	tbl := Table{
		Name:    "settings",
		Columns: []string{"category", "payload"},
		JSONPaths: map[string]JSONRule{
			"payload": {RedactFor: func(map[string]any) ([]string, bool) { return nil, false }},
		},
	}

	img, flags := tbl.image(map[string]any{
		"category": "brand-new",
		"payload":  `{"someSecret":"sealed:xyz","harmless":1}`,
	})

	if flags["redactorUnknownCategory"] != true {
		t.Fatalf("esperava a flag redactorUnknownCategory, veio %#v", flags)
	}
	if doc, present := img["payload"]; present {
		if m, ok := doc.(map[string]any); !ok || len(m) != 0 {
			t.Fatalf("categoria desconhecida tem de podar tudo, veio %#v", doc)
		}
	}
}

// The jsonb column arrives from database/sql as []byte as often as string.
func TestImageAcceptsJSONAsBytes(t *testing.T) {
	tbl := Table{
		Name:    "settings",
		Columns: []string{"payload"},
		JSONPaths: map[string]JSONRule{
			"payload": {RedactFor: func(map[string]any) ([]string, bool) { return nil, true }},
		},
	}

	img, _ := tbl.image(map[string]any{"payload": []byte(`{"a":1}`)})

	doc, ok := img["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload devia ser um documento, veio %#v", img["payload"])
	}
	if doc["a"] == nil {
		t.Fatalf("documento vazio: %#v", doc)
	}
}

// Unparseable jsonb is dropped, not passed through: passing it through would put
// raw, unredacted bytes into a table one permission can read.
func TestImageDropsUnparseableJSON(t *testing.T) {
	tbl := Table{
		Name:    "settings",
		Columns: []string{"payload"},
		JSONPaths: map[string]JSONRule{
			"payload": {RedactFor: func(map[string]any) ([]string, bool) { return nil, true }},
		},
	}

	img, flags := tbl.image(map[string]any{"payload": "not json at all"})

	if v, present := img["payload"]; present {
		if m, ok := v.(map[string]any); !ok || len(m) != 0 {
			t.Fatalf("an unreadable jsonb has to be dropped, got %#v", v)
		}
	}
	if flags["redactorUnparseable"] != true {
		t.Fatalf("esperava a flag redactorUnparseable, veio %#v", flags)
	}
}
