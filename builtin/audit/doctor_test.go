package audit

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func doctorCap(t *testing.T, report func() view.View) plugin.Capability {
	t.Helper()
	for _, c := range Plugin(testCatalog, report).Capabilities {
		if c.ID == "audit.doctor" {
			return c
		}
	}
	t.Fatal("audit.doctor is not declared")
	return plugin.Capability{}
}

// The capability is the injected report, whole: what `rta doctor` prints is
// what the TUI shows, from one function, so the two cannot drift.
func TestDoctorIsTheInjectedReport(t *testing.T) {
	want := view.Text{Body: "the environment, as the doctor sees it"}
	c := doctorCap(t, func() view.View { return want })
	if !c.HumanOnly || !c.NoPreview {
		t.Fatalf("audit.doctor is HumanOnly=%v NoPreview=%v; it names where credentials live and is not a tile", c.HumanOnly, c.NoPreview)
	}
	got, err := c.Run(context.Background(), plugin.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("run = %+v, want the report unchanged", got)
	}
}

// A surface that did not wire the report says so, by code, rather than
// answering with an empty page that reads as a healthy machine.
func TestDoctorSaysWhenItIsUnwired(t *testing.T) {
	c := doctorCap(t, nil)
	_, err := c.Run(context.Background(), plugin.Request{})
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "audit.doctor.unwired" {
		t.Fatalf("err = %v, want audit.doctor.unwired", err)
	}
}

// The verbs of the mixed plugins join the block beside the plugins denied
// whole: audit.doctor from a plugin that mixes, never kv.copy, whose plugin
// the hand-kept list already names in full.
func TestTheDenyBlockDerivesTheVerbsOfMixedPlugins(t *testing.T) {
	if got := HumanOnlyVerbs(testCatalog); !reflect.DeepEqual(got, []string{"audit doctor"}) {
		t.Errorf("verbs = %v, want audit doctor alone", got)
	}
	block := denyBlock(testCatalog)
	for _, want := range []string{`"Bash(rta audit doctor:*)"`, `"Bash(rta kv:*)"`, `"Bash(rta grant:*)"`} {
		if !strings.Contains(block, want) {
			t.Errorf("the block is missing %s:\n%s", want, block)
		}
	}
	if strings.Contains(block, "kv copy") {
		t.Errorf("the block names a verb of a plugin it already denies whole:\n%s", block)
	}
}
