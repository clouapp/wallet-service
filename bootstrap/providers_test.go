package bootstrap_test

import (
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/packages/activitylog"
)

// packages/activitylog ships its own ServiceProvider: Register binds the manual
// API, Boot installs the gorm capture plugin. Booting the application must run
// both.
func TestBoot_Registers_TheActivityLogServiceProvider(t *testing.T) {
	instance, err := facades.App().Make(activitylog.Binding)
	if err != nil {
		t.Fatalf("%q is not bound, so the activitylog ServiceProvider did not run Register: %v", activitylog.Binding, err)
	}
	if _, ok := instance.(*activitylog.ActivityLog); !ok {
		t.Fatalf("%q resolved to %T, want *activitylog.ActivityLog", activitylog.Binding, instance)
	}
	if activitylog.App != app {
		t.Fatal("activitylog.App is not the booted application: Register did not run")
	}
}
