package architecture

import (
	"strings"
	"testing"
)

// TestImportDirection checks every module import against the layer allow-set.
// An entry is one package edge: "from-dir → to-dir (from-layer → to-layer)".
func TestImportDirection(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.Files {
		if file.IsGenerated {
			continue
		}
		from := Layer(file.Dir)
		if from == "" {
			continue
		}
		for _, importPath := range file.Imports() {
			target, inModule := module.RelativeOf(importPath)
			if !inModule {
				continue
			}
			to := Layer(target)
			if to == "" {
				continue
			}
			check, suffix := CheckImport, ""
			if file.IsTest {
				check, suffix = CheckTestImport, " [test]"
			}
			if err := check(from, to); err != nil {
				violations.Add("%s → %s (%s → %s)%s", file.Dir, target, from, to, suffix)
			}
		}
	}
	Report(t, &violations)
}

// TestLayerCoversEveryZoneOfTheRepository reports a directory holding Go code
// that no layer claims: it would escape every import rule.
func TestLayerCoversEveryZoneOfTheRepository(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	seen := map[string]bool{}
	for _, file := range module.Files {
		if seen[file.Dir] {
			continue
		}
		seen[file.Dir] = true
		if Layer(file.Dir) == "" {
			violations.Add("%s has no layer", file.Dir)
		}
	}
	Report(t, &violations)
}

// TestThePackageDoesNotReachTheModule reports a packages/* (today pkg/*)
// file importing anything of the module: such a package is meant to be
// installable outside the project.
func TestThePackageDoesNotReachTheModule(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.ProductionFiles("pkg", "packages") {
		for _, importPath := range file.Imports() {
			if target, inModule := module.RelativeOf(importPath); inModule {
				violations.Add("%s imports %s", file.Path, target)
			}
		}
	}
	Report(t, &violations)
}

// TestProductionCodeMayNotImportTheGeneratedMocks reports production code
// importing the test harness (tests/...), and production packages that exist
// only to serve tests.
func TestProductionCodeMayNotImportTheGeneratedMocks(t *testing.T) {
	module := sharedModule(t)
	var violations Violations
	for _, file := range module.ProductionFiles() {
		if Layer(file.Dir) == LayerTests || Layer(file.Dir) == LayerTools {
			continue
		}
		for _, importPath := range file.Imports() {
			target, inModule := module.RelativeOf(importPath)
			if inModule && Layer(target) == LayerTests {
				violations.Add("%s imports %s", file.Path, target)
			}
		}
		if strings.HasSuffix(file.Dir, "/testutil") {
			violations.Add("%s is a test helper inside production code", file.Path)
		}
	}
	Report(t, &violations)
}

func TestLayer_MapsEveryKnownZone(t *testing.T) {
	cases := map[string]string{
		".":                               LayerMain,
		"app/models":                      LayerModels,
		"app/http/controllers":            LayerHTTP,
		"app/http/controllers/testutil":   LayerHTTP,
		"app/services/sweep":              LayerServices,
		"app/services/ingest/providers":   LayerServices,
		"app/adapters/chain/evm":          LayerAdapters,
		"app/repositories/internal/db":    LayerRepositories,
		"app/listeners":                   LayerEvents,
		"pkg/amount":                      LayerPackages,
		"packages/featureflag":            LayerPackages,
		"database/migrations":             LayerDatabase,
		"tests/mocks":                     LayerTests,
		"tools/e2e-funder":                LayerTools,
		"app/providers":                   LayerProviders,
		"app/http/requests":               LayerHTTP,
		"app/console/commands":            LayerConsole,
		"routes":                          LayerRoutes,
		"config":                          LayerConfig,
		"bootstrap":                       LayerBootstrap,
		"app/container":                   LayerContainer,
		"app/policies":                    LayerPolicies,
		"app/dtos":                        LayerDTOs,
		"app/mails":                       LayerMails,
		"app/jobs":                        LayerJobs,
		"app/rules":                       LayerRules,
		"app/facades":                     LayerFacades,
		"docs":                            LayerDocs,
		"scripts/e2e":                     LayerTools,
		"app/http/middleware":             LayerHTTP,
		"app/services/withdrawalevents":   LayerServices,
		"database/seeders":                LayerDatabase,
		"tests/feature/support/testenv":   LayerTests,
		"app/repositories":                LayerRepositories,
		"app/events":                      LayerEvents,
		"app/services/chain/testdata/sol": LayerServices,
	}
	for dir, want := range cases {
		if got := Layer(dir); got != want {
			t.Errorf("Layer(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestLayer_LeavesAnUnknownZoneUnclassified(t *testing.T) {
	for _, dir := range []string{"app", "app/unknown", "lib", "appendix", "configs", "pkgx/amount"} {
		if got := Layer(dir); got != "" {
			t.Errorf("Layer(%q) = %q, want unclassified", dir, got)
		}
	}
}

func TestCheckImport_SyntheticEdges(t *testing.T) {
	refused := [][2]string{
		{LayerModels, LayerServices},
		{LayerModels, LayerRepositories},
		{LayerHTTP, LayerRepositories},
		{LayerHTTP, LayerAdapters},
		{LayerServices, LayerHTTP},
		{LayerServices, LayerRepositories},
		{LayerServices, LayerAdapters},
		{LayerServices, LayerContainer},
		{LayerPolicies, LayerServices},
		{LayerPackages, LayerModels},
		{LayerPackages, LayerPackages},
		{LayerRules, LayerContainer},
		{LayerConfig, LayerModels},
		{LayerContainer, LayerServices},
		{LayerRoutes, LayerRepositories},
		{LayerDTOs, LayerServices},
		{LayerServices, LayerTests},
		{LayerMain, LayerTests},
		{"", LayerModels},
		{LayerModels, ""},
		{"unknown-layer", LayerModels},
	}
	for _, edge := range refused {
		if CheckImport(edge[0], edge[1]) == nil {
			t.Errorf("CheckImport(%q, %q) allowed a refused edge", edge[0], edge[1])
		}
	}
	admitted := [][2]string{
		{LayerHTTP, LayerServices},
		{LayerHTTP, LayerFacades},
		{LayerHTTP, LayerHTTP},
		{LayerHTTP, LayerPackages},
		{LayerServices, LayerModels},
		{LayerServices, LayerServices},
		{LayerServices, LayerPackages},
		{LayerAdapters, LayerServices},
		{LayerRepositories, LayerServices},
		{LayerRoutes, LayerHTTP},
		{LayerProviders, LayerRepositories},
		{LayerProviders, LayerAdapters},
		{LayerDTOs, LayerModels},
		{LayerPolicies, LayerModels},
		{LayerMain, LayerBootstrap},
		{LayerTests, LayerServices},
	}
	for _, edge := range admitted {
		if err := CheckImport(edge[0], edge[1]); err != nil {
			t.Errorf("CheckImport(%q, %q) refused an admitted edge: %v", edge[0], edge[1], err)
		}
	}
}

func TestCheckTestImport_AdmitsTheHarnessButNotPersistenceFromHTTP(t *testing.T) {
	if err := CheckTestImport(LayerServices, LayerTests); err != nil {
		t.Errorf("a service test must reach the harness: %v", err)
	}
	if err := CheckTestImport(LayerHTTP, LayerBootstrap); err != nil {
		t.Errorf("an HTTP test must boot the app: %v", err)
	}
	if CheckTestImport(LayerHTTP, LayerRepositories) == nil {
		t.Error("an HTTP test reaching repositories must be refused")
	}
	if CheckTestImport(LayerModels, LayerServices) == nil {
		t.Error("a models test reaching services must be refused")
	}
}

func TestEveryLayerHasAnAllowRow(t *testing.T) {
	for _, layer := range zones {
		if _, ok := allowed[layer]; !ok {
			t.Errorf("layer %q has no row in the allow table", layer)
		}
	}
}
