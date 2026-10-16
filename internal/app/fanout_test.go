package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A read that names an environment and none of its targets runs on each target,
// one section apiece. What it is not allowed to be is as much of the design as
// what it does: reads only, a typed value that the targets state themselves
// still has to name its target, and a failure on one target is not silence
// about the others (fanout.go).

func fanOutRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	host := plugin.Field{Name: "host", Type: plugin.String, Default: "localhost", Config: "host", Local: true, Help: "host"}
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "db", Summary: "db plugin",
		Capabilities: []plugin.Capability{
			{
				ID: "db.status", Summary: "status", Safety: plugin.Read, Inputs: []plugin.Field{host},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					if req.String("host") == "down.internal" {
						return nil, view.Errorf("db.refused", "nothing listens on down.internal")
					}
					return view.Text{Body: "reached " + req.String("host")}, nil
				},
			},
			{
				ID: "db.reset", Summary: "reset", Safety: plugin.Write, Inputs: []plugin.Field{host},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					return view.Text{Body: "reset " + req.String("host")}, nil
				},
			},
			{
				ID: "db.panic", Summary: "fails everywhere", Safety: plugin.Read, Inputs: []plugin.Field{host},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return nil, errors.New("always")
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

const fanOutConfig = `profiles:
  shards:
    plugins:
      db/a:
        set:
          host: a.internal
      db/b:
        set:
          host: b.internal
  mixed:
    plugins:
      db/up:
        set:
          host: up.internal
      db/down:
        set:
          host: down.internal
  one:
    plugins:
      db:
        set:
          host: only.internal
`

func TestABareEnvironmentRunsAReadOnEveryTargetItHolds(t *testing.T) {
	reg := fanOutRegistry(t)
	out, errOut, err := runWith(t, reg, fanOutConfig, "db", "status", "--profile", "shards")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	for _, want := range []string{"SHARDS/A", "reached a.internal", "SHARDS/B", "reached b.internal"} {
		if !strings.Contains(out, want) {
			t.Errorf("the page does not carry %q:\n%s", want, out)
		}
	}
}

func TestTheEnvironmentThatIsOnFansOutWithNoFlagAtAll(t *testing.T) {
	on(t, "shards", nil)
	out, errOut, err := runSwitchedOutcome(t, fanOutRegistry(t), fanOutConfig, "db", "status")
	if err != nil || !strings.Contains(out, "reached a.internal") || !strings.Contains(out, "reached b.internal") {
		t.Fatalf("%q %v\n%s", out, err, errOut)
	}
}

func TestANamedTargetStillRunsAlone(t *testing.T) {
	out, _, err := runWith(t, fanOutRegistry(t), fanOutConfig, "db", "status", "--profile", "shards/a")
	if err != nil || !strings.Contains(out, "reached a.internal") || strings.Contains(out, "b.internal") {
		t.Errorf("%q %v", out, err)
	}
}

// A change that goes to every connection of an environment because one word was
// left out is what the refusal exists for.
func TestAWriteIsStillRefusedForAnEnvironmentWithSeveralTargets(t *testing.T) {
	_, errOut, err := runWith(t, fanOutRegistry(t), fanOutConfig, "db", "reset", "--profile", "shards", "--yes")
	if err == nil || !strings.Contains(errOut, "core.profile.instance.required") {
		t.Fatalf("a write fanned out or failed otherwise: %v\n%s", err, errOut)
	}
}

// One typed host sent to every target in place of what each says is N calls to
// one place.
func TestAValueTheTargetsStateThemselvesStillHasToNameATarget(t *testing.T) {
	_, errOut, err := runWith(t, fanOutRegistry(t), fanOutConfig, "db", "status", "--profile", "shards", "--host", "x.internal")
	if err == nil || !strings.Contains(errOut, "core.profile.instance.required") {
		t.Fatalf("a typed host fanned out: %v\n%s", err, errOut)
	}
}

func TestAFailureOnOneTargetIsOnThePageAndInTheExitCode(t *testing.T) {
	out, errOut, err := runWith(t, fanOutRegistry(t), fanOutConfig, "db", "status", "--profile", "mixed")
	if err == nil {
		t.Fatal("a partial answer exited zero")
	}
	for _, want := range []string{"reached up.internal", "mixed/down", "nothing listens on down.internal"} {
		if !strings.Contains(out+errOut, want) {
			t.Errorf("output does not carry %q:\n%s%s", want, out, errOut)
		}
	}
	if !strings.Contains(errOut, "1 of 2 targets failed") {
		t.Errorf("no summary of the failure:\n%s", errOut)
	}
}

func TestAnEnvironmentWithOneConnectionRunsAsItAlwaysDid(t *testing.T) {
	out, _, err := runWith(t, fanOutRegistry(t), fanOutConfig, "db", "status", "--profile", "one")
	if err != nil || strings.TrimSpace(out) != "reached only.internal" {
		t.Errorf("%q %v", out, err)
	}
}

func TestLeavingTheEnvironmentDoesNotFanOut(t *testing.T) {
	on(t, "shards", nil)
	out, _, err := runSwitchedOutcome(t, fanOutRegistry(t), fanOutConfig, "db", "status", "--no-profile")
	if err != nil || strings.Contains(out, "SHARDS/") {
		t.Errorf("%q %v", out, err)
	}
}

// The completion follows the environment that is on: its targets in full, the
// others only as `name/`, and everything once nothing is on or a name and a slash
// are typed.
func TestProfileCompletionOffersTheEnvironmentThatIsOnAndOnlyTheNamesOfTheRest(t *testing.T) {
	reg := fanOutRegistry(t)
	status, _ := reg.Capability("db.status")
	reset, _ := reg.Capability("db.reset")
	cfg, err := config.Parse([]byte(fanOutConfig))
	if err != nil {
		t.Fatal(err)
	}

	refs := func(c plugin.Capability, active, typed string) (out []string, noSpace bool) {
		items, dir := profileFlagCompletions(cfg, c, active, typed)
		for _, it := range items {
			out = append(out, strings.SplitN(it, "\t", 2)[0])
		}
		return out, dir&cobra.ShellCompDirectiveNoSpace != 0
	}

	got, noSpace := refs(status, "shards", "")
	want := []string{"shards", "shards/a", "shards/b"}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("the environment that is on does not offer %q: %v", w, got)
		}
	}
	if slices.Contains(got, "mixed/up") || slices.Contains(got, "mixed/down") {
		t.Errorf("another environment's targets are offered while shards is on: %v", got)
	}
	if !slices.Contains(got, "mixed/") || !noSpace {
		t.Errorf("the other environments are not offered as name/ with no space: %v %v", got, noSpace)
	}

	if got, _ := refs(status, "shards", "mixed/"); !slices.Contains(got, "mixed/down") {
		t.Errorf("typing the name and a slash does not list its targets: %v", got)
	}
	if got, _ := refs(status, "", ""); !slices.Contains(got, "mixed/down") || !slices.Contains(got, "shards/a") {
		t.Errorf("with nothing on, everything is offered: %v", got)
	}
	// A bare name over several targets is a read's, and a write's refusal.
	if got, _ := refs(reset, "shards", ""); slices.Contains(got, "shards") {
		t.Errorf("a write is offered the bare name the refusal rejects: %v", got)
	}
}
