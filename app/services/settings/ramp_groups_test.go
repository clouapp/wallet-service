package settings

import (
	engine "github.com/macrowallets/waas/packages/rampengine/app/settings"
	"strings"
	"testing"
)

func TestRampSettings_PreserveEverySourceKeyAndSeparateEnvironments(t *testing.T) {
	for _, source := range engine.RampGroups() {
		for _, mode := range []string{"sandbox", "production"} {
			g, ok := FindGroup(RampGroupName(source.Name, mode))
			if !ok {
				t.Fatalf("missing %s/%s", source.Name, mode)
			}
			if g.Scope != ScopePlatform || g.Section != "ramps" || g.Block != mode {
				t.Fatalf("unscoped group %+v", g)
			}
			if len(g.Settings) != len(source.Settings) {
				t.Fatalf("dropped settings in %s", g.Name)
			}
			for i, field := range source.Settings {
				actual := g.Settings[i]
				if actual.Key != field.Key || actual.Secret != field.Secret {
					t.Fatalf("changed credential metadata %s/%s", g.Name, field.Key)
				}
				if actual.Secret && actual.Default != nil && strings.TrimSpace(stringValue(actual.Default())) != "" {
					t.Fatal("source credential copied")
				}
				if field.Key == "mode" && actual.Default() != mode {
					t.Fatal("provider mode is not account-bound")
				}
			}
		}
	}
}
func stringValue(v any) string { s, _ := v.(string); return s }
