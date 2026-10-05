package tui

import (
	"slices"
	"testing"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// "read · grant" in a column of permissions reads as something the reader has
// been given. It means the opposite: an agent has to be given one first.
func TestThePermissionLabelSaysWhoNeedsTheGrant(t *testing.T) {
	for _, tc := range []struct {
		c    plugin.Capability
		want string
	}{
		{plugin.Capability{ID: "a.b", Safety: plugin.Read}, "read"},
		{plugin.Capability{ID: "a.b", Safety: plugin.Write, NeedsGrant: true}, "write · agents need grant"},
		{plugin.Capability{ID: "a.b", Safety: plugin.Destructive}, "destructive · agents need grant"},
		{plugin.Capability{ID: "a.b", Safety: plugin.Write, HumanOnly: true}, "write · not for agents"},
	} {
		if got := permissionText(tc.c); got != tc.want {
			t.Errorf("permissionText(%s %s) = %q, want %q", tc.c.ID, tc.c.Safety, got, tc.want)
		}
	}
}

// The catalogue opens on its first page, and that page was seven rows of "not
// for agents" administration. The plugins only the person at the keyboard can
// use come after the rest, and nothing is lost: the same capabilities, the same
// headers, in the same order among themselves.
func TestTheCatalogueEndsWithThePluginsOnlyYouCanUse(t *testing.T) {
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	var headers []string
	caps := 0
	for _, it := range catalogueItems(reg) {
		switch v := it.(type) {
		case pluginHeader:
			headers = append(headers, v.p.Name)
		case capItem:
			caps++
		}
	}
	if caps != len(reg.Capabilities()) {
		t.Errorf("the catalogue lists %d capabilities of %d", caps, len(reg.Capabilities()))
	}
	if len(headers) != len(reg.Plugins()) {
		t.Errorf("the catalogue has %d plugin headers for %d plugins", len(headers), len(reg.Plugins()))
	}
	if headers[0] == "agent" {
		t.Errorf("the catalogue still opens on the agent administration: %v", headers)
	}
	yours := []string{"agent", "grant", "lock", "operator", "pkg"}
	if tail := headers[len(headers)-len(yours):]; !slices.Equal(tail, yours) {
		t.Errorf("the catalogue ends with %v, want the plugins only you can use, %v", tail, yours)
	}
	if !slices.IsSorted(headers[:len(headers)-len(yours)]) {
		t.Errorf("the rest of the catalogue lost its order: %v", headers)
	}
}
