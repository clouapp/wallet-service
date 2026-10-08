package architecture

import (
	"fmt"
	"strings"
)

// Layers of the module. A directory belongs to exactly one; Layer returns ""
// for a directory no row claims, which TestLayerCoversEveryZoneOfTheRepository
// reports.
const (
	LayerMain         = "main"
	LayerBootstrap    = "bootstrap"
	LayerConfig       = "config"
	LayerRoutes       = "routes"
	LayerContainer    = "container"
	LayerProviders    = "providers"
	LayerHTTP         = "http"
	LayerPolicies     = "policies"
	LayerServices     = "services"
	LayerAdapters     = "adapters"
	LayerRepositories = "repositories"
	LayerModels       = "models"
	LayerMails        = "mails"
	LayerConsole      = "console"
	LayerJobs         = "jobs"
	LayerEvents       = "events"
	LayerRules        = "rules"
	LayerFacades      = "facades"
	LayerDatabase     = "database"
	LayerPackages     = "packages"
	LayerDocs         = "docs"
	LayerTests        = "tests"
	LayerTools        = "tools"
)

// zones maps a module-relative directory prefix to its layer. The longest
// matching prefix wins.
var zones = map[string]string{
	".":                LayerMain,
	"bootstrap":        LayerBootstrap,
	"config":           LayerConfig,
	"routes":           LayerRoutes,
	"app/container":    LayerContainer,
	"app/providers":    LayerProviders,
	"app/http":         LayerHTTP,
	"app/policies":     LayerPolicies,
	"app/services":     LayerServices,
	"app/adapters":     LayerAdapters,
	"app/repositories": LayerRepositories,
	"app/models":       LayerModels,
	"app/mails":        LayerMails,
	"app/console":      LayerConsole,
	"app/jobs":         LayerJobs,
	"app/events":       LayerEvents,
	"app/rules":        LayerRules,
	"app/facades":      LayerFacades,
	"database":         LayerDatabase,
	"pkg":              LayerPackages,
	"packages":         LayerPackages,
	"docs":             LayerDocs,
	"tests":            LayerTests,
	"tools":            LayerTools,
	"scripts":          LayerTools,
}

// everything is the allow-set of a composition layer: it may import any
// layer except the test harness.
var everything = []string{
	LayerBootstrap, LayerConfig, LayerRoutes, LayerContainer, LayerProviders, LayerHTTP,
	LayerPolicies, LayerServices, LayerAdapters, LayerRepositories, LayerModels,
	LayerMails, LayerConsole, LayerJobs, LayerEvents, LayerRules, LayerFacades, LayerDatabase,
	LayerPackages, LayerDocs,
}

// allowed is the TARGET table of .ai/guidelines/controllers-and-services.md and
// the alignment plan §3.1: which layers a production file of each layer may
// import. packages/* (today pkg/*) is installable outside the project, so every
// layer may import it and it imports nothing of the module.
var allowed = map[string][]string{
	LayerMain:         everything,
	LayerBootstrap:    everything,
	LayerProviders:    everything,
	LayerConfig:       {},
	LayerContainer:    {},
	LayerModels:       {},
	LayerPackages:     {},
	LayerDocs:         {},
	LayerPolicies:     {LayerModels},
	LayerMails:        {LayerModels},
	LayerServices:     {LayerServices, LayerModels, LayerMails, LayerPolicies},
	LayerAdapters:     {LayerAdapters, LayerServices, LayerModels},
	LayerRepositories: {LayerRepositories, LayerServices, LayerModels},
	LayerHTTP:         {LayerHTTP, LayerContainer, LayerModels, LayerServices, LayerMails, LayerPolicies, LayerFacades},
	LayerFacades:      {},
	LayerRoutes:       {LayerContainer, LayerHTTP, LayerServices, LayerModels, LayerDocs},
	LayerRules:        {LayerModels},
	LayerConsole:      {LayerConsole, LayerContainer, LayerServices, LayerModels},
	LayerJobs:         {LayerContainer, LayerServices, LayerModels},
	LayerEvents:       {LayerEvents, LayerJobs, LayerContainer, LayerServices, LayerModels},
	LayerDatabase:     {LayerDatabase, LayerConfig, LayerServices, LayerAdapters, LayerRepositories, LayerModels},
	LayerTests:        append([]string{LayerTests}, everything...),
	LayerTools:        append([]string{LayerTools}, everything...),
}

// testOnlyAllowed is what a *_test.go file may import on top of its layer's
// allow-set: the harness, and what it takes to boot the app.
var testOnlyAllowed = []string{LayerTests, LayerBootstrap, LayerConfig}

// Layer returns the layer of a module-relative directory, or "" when no zone
// claims it.
func Layer(dir string) string {
	best, bestLength := "", -1
	for prefix, layer := range zones {
		matches := dir == prefix || (prefix != "." && strings.HasPrefix(dir, prefix+"/"))
		if matches && len(prefix) > bestLength {
			best, bestLength = layer, len(prefix)
		}
	}
	return best
}

// CheckImport returns an error when a production file of layer from may not
// import layer to.
func CheckImport(from, to string) error {
	if from == "" || to == "" {
		return fmt.Errorf("unclassified edge %q → %q", from, to)
	}
	rule, known := allowed[from]
	if !known {
		return fmt.Errorf("layer %q has no row in the allow table", from)
	}
	if containsLayer(rule, to) || (to == LayerPackages && from != LayerPackages) {
		return nil
	}
	return fmt.Errorf("%s may not import %s", from, to)
}

// CheckTestImport returns an error when a *_test.go file of layer from may not
// import layer to.
func CheckTestImport(from, to string) error {
	if CheckImport(from, to) == nil || containsLayer(testOnlyAllowed, to) {
		return nil
	}
	return fmt.Errorf("%s tests may not import %s", from, to)
}

func containsLayer(layers []string, layer string) bool {
	for _, candidate := range layers {
		if candidate == layer {
			return true
		}
	}
	return false
}
