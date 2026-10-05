package app

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/view"
)

func TestDoctorKeepsARowForAFailedPluginInItsReport(t *testing.T) {
	withFailedPlugins(t, helloFailed)
	var found bool
	for _, r := range doctorReport(registry.New()).(view.Table).Rows {
		if r[0] == "plugin hello" {
			found = r[1] == "warn" && strings.Contains(r[2], "failed to start")
		}
	}
	if !found {
		t.Error("doctor has no warning row for the plugin that failed to start")
	}
}
