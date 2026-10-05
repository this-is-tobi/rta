package pluginconf

import (
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A key that several capabilities read as a duration keeps the widest of their
// ranges, as one read as a number does. The bounds are text with a unit, which
// widest read as no number at all, so the shared key came out with no range:
// `rta profile set --set wait=1h` was accepted against readers that all stop at
// five minutes, and every call then clamped it without a word.
func TestSharedFieldIsTheWidestOfDurationReaders(t *testing.T) {
	got := SharedField([]plugin.Field{
		{Name: "wait", Type: plugin.Duration, Min: "1s", Max: "1m"},
		{Name: "wait", Type: plugin.Duration, Min: "500ms", Max: "5m"},
		{Name: "wait", Type: plugin.Duration, Min: "2s", Max: "90s"},
	})
	if got.Min != "500ms" || got.Max != "5m" {
		t.Errorf("merged = min %v max %v, want 500ms and 5m", got.Min, got.Max)
	}
	got = SharedField([]plugin.Field{
		{Name: "wait", Type: plugin.Duration, Min: "1s", Max: "1h"},
		{Name: "wait", Type: plugin.Duration, Min: "1m", Max: "90m"},
	})
	if got.Max != "90m" {
		t.Errorf("merged max %v, want 90m, which is longer than 1h whatever the digits say as text", got.Max)
	}
	got = SharedField([]plugin.Field{
		{Name: "wait", Type: plugin.Duration, Min: "1s", Max: "1m"},
		{Name: "wait", Type: plugin.Duration, Max: "5m"},
	})
	if got.Min != nil || got.Max != "5m" {
		t.Errorf("merged = min %v max %v, want a reader with no minimum to remove it", got.Min, got.Max)
	}
}
