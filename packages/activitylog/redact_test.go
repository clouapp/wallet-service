package activitylog

import (
	"strings"
	"testing"
)

func TestProjectedSelectHidesASecretValue(t *testing.T) {
	tbl := Table{
		Columns: []string{"group", "key", "value"},
		TextRedaction: TextRedaction{
			Column:       "value",
			MatchColumns: []string{"group", "key"},
			Pairs:        [][]string{{"mail_smtp", "password"}},
		},
	}

	got, err := tbl.projectedSelect([]string{"group", "key", "value", "id"}, func(name string) string {
		return `"` + name + `"`
	})
	if err != nil {
		t.Fatalf("projectedSelect: %v", err)
	}
	for _, part := range []string{
		`CASE WHEN ("group", "key") IN (('mail_smtp', 'password')) THEN NULL ELSE "value" END AS "value"`,
		`END AS "valueSet"`,
		`btrim("value")`,
	} {
		if !strings.Contains(got, part) {
			t.Fatalf("select = %s", got)
		}
	}
}

func TestProjectedSelectLeavesAnUnredactedTableAlone(t *testing.T) {
	tbl := Table{Columns: []string{"email"}}
	got, err := tbl.projectedSelect([]string{"email", "id"}, func(name string) string {
		return `"` + name + `"`
	})
	if err != nil {
		t.Fatalf("projectedSelect: %v", err)
	}
	if got != `"email", "id"` {
		t.Fatalf("select = %s", got)
	}
}

func TestRegisterRejectsAnUnsafeRedactionToken(t *testing.T) {
	freshRegistry(t)
	err := Register(Table{
		Name:    "settings",
		Columns: []string{"value"},
		TextRedaction: TextRedaction{
			Column:       "value",
			MatchColumns: []string{"group", "key"},
			Pairs:        [][]string{{"mail_smtp", "pass word"}},
		},
	})
	if err == nil {
		t.Fatal("expected an unsafe token to be refused")
	}
}

func TestImageReplacesASecretValueWithValueSet(t *testing.T) {
	tbl := Table{
		Columns: []string{"group", "key", "value"},
		TextRedaction: TextRedaction{
			Column:       "value",
			MatchColumns: []string{"group", "key"},
			Pairs:        [][]string{{"mail_smtp", "password"}},
		},
	}

	secret, flags := tbl.image(map[string]any{
		"group": "mail_smtp",
		"key":   "password",
		"value": "enc:v1:ciphertext-that-must-not-be-stored",
	})
	if len(flags) != 0 {
		t.Fatalf("flags = %#v", flags)
	}
	if _, leaked := secret["value"]; leaked {
		t.Fatalf("value leaked: %#v", secret)
	}
	if secret["valueSet"] != true || secret["key"] != "password" {
		t.Fatalf("image = %#v", secret)
	}

	plain, _ := tbl.image(map[string]any{
		"group": "mail_smtp",
		"key":   "host",
		"value": "127.0.0.1",
	})
	if plain["value"] != "127.0.0.1" {
		t.Fatalf("non-secret value = %#v", plain)
	}
	if _, extra := plain["valueSet"]; extra {
		t.Fatalf("non-secret gained valueSet: %#v", plain)
	}
}

func TestImageUsesTheBooleanWhenTheSecretTextWasNotRead(t *testing.T) {
	tbl := Table{
		Columns: []string{"key", "value"},
		TextRedaction: TextRedaction{
			Column:       "value",
			MatchColumns: []string{"group", "key"},
			Pairs:        [][]string{{"mail_smtp", "password"}},
		},
	}

	img, _ := tbl.image(map[string]any{
		"group":    "mail_smtp",
		"key":      "password",
		"value":    nil,
		"valueSet": true,
	})
	if img["valueSet"] != true {
		t.Fatalf("image = %#v", img)
	}
	if _, leaked := img["value"]; leaked {
		t.Fatal("null value was recorded")
	}
}
