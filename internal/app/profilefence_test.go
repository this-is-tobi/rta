package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// fenceCLIRegistry is a plugin shaped like cnpg: a scope a person may name and
// the switch that reads past it, and a handler that says what it was handed.
func fenceCLIRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "app", Summary: "app", Capabilities: []plugin.Capability{{
		ID: "app.status", Summary: "status", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "namespace", Type: plugin.String, Config: "namespace", Help: "namespace"},
			{Name: "all-namespaces", Type: plugin.Bool, Config: "all-namespaces", Widens: "namespace", Help: "every namespace"},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			return view.Text{Body: fmt.Sprintf("ran namespace=%q all=%v", req.String("namespace"), req.Bool("all-namespaces"))}, nil
		},
	}}}); err != nil {
		t.Fatal(err)
	}
	return reg
}

const fencedCLI = `
output: pretty
profiles:
  staging:
    plugins:
      app:
        set:
          namespace: staging
        only:
          namespace: [staging, staging-jobs]
  pinned:
    plugins:
      app:
        only:
          namespace: [only-this]
  shared:
    plugins:
      app:
        only:
          namespace: [alpha, beta]
`

// A person at a terminal is held to the fence too: the profile says "this
// environment is that namespace and no other" to whoever is typing, and the
// flag they typed is a value it has an answer to. The refusal names the input,
// what the connection reaches and the ways across.
func TestACallOutsideTheFenceIsRefusedAtTheTerminalBeforeItRuns(t *testing.T) {
	reg := fenceCLIRegistry(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"another value", []string{"app", "status", "--profile", "staging", "--namespace", "prod"},
			`--namespace is outside what profile "staging" reaches: staging, staging-jobs`},
		{"a blank", []string{"app", "status", "--profile", "staging", "--namespace", ""},
			`--namespace is blank, and profile "staging" reaches only: staging, staging-jobs`},
		{"the switch", []string{"app", "status", "--profile", "staging", "--all-namespaces"},
			`--all-namespaces reads past --namespace, which profile "staging" fences to: staging, staging-jobs`},
		{"no value for a list of two", []string{"app", "status", "--profile", "shared"},
			`--namespace is blank, and profile "shared" reaches only: alpha, beta`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := runWith(t, reg, fencedCLI, tc.args...)
			if err == nil {
				t.Fatalf("the call ran: %s", out)
			}
			if !strings.Contains(errOut, tc.want) {
				t.Errorf("stderr = %q, want %q", errOut, tc.want)
			}
			if !strings.Contains(errOut, "rta profile set") {
				t.Errorf("the refusal does not say how to cross: %q", errOut)
			}
			if strings.Contains(out, "ran namespace") {
				t.Errorf("the handler ran: %s", out)
			}
		})
	}
}

// And nothing changes for a call that stays inside it.
func TestACallInsideTheFenceRunsAsItDid(t *testing.T) {
	reg := fenceCLIRegistry(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"app", "status", "--profile", "staging"}, `ran namespace="staging" all=false`},
		{[]string{"app", "status", "--profile", "staging", "--namespace", "staging-jobs"}, `ran namespace="staging-jobs" all=false`},
		// The one value a fence allows is the input, when nothing else says.
		{[]string{"app", "status", "--profile", "pinned"}, `ran namespace="only-this" all=false`},
		{[]string{"app", "status", "--profile", "shared", "--namespace", "beta"}, `ran namespace="beta" all=false`},
	} {
		out, errOut, err := runWith(t, reg, fencedCLI, tc.args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", tc.args, err, errOut)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%v printed %q, want %q", tc.args, out, tc.want)
		}
	}
}

// The environment switched on with `rta use` is held to its fence the same way
// as one named with --profile: it is the same connection, resolved by another
// route, and the route is the thing a fence must not depend on.
func TestTheSwitchedOnEnvironmentIsFencedToo(t *testing.T) {
	reg := fenceCLIRegistry(t)
	run := session(t, reg)
	if err := os.WriteFile(config.Path(), []byte(fencedCLI), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := run("use", "staging"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	_, errOut, err := run("app", "status", "--namespace", "prod")
	if err == nil || !strings.Contains(errOut, `--namespace is outside what profile "staging" reaches`) {
		t.Fatalf("err = %v, stderr = %q", err, errOut)
	}
	out, errOut, err := run("app", "status", "--namespace", "staging-jobs")
	if err != nil || !strings.Contains(out, `ran namespace="staging-jobs"`) {
		t.Fatalf("a call inside the fence under the switch: %v %q %q", err, out, errOut)
	}
}

// An export for the profile is a value the fence holds like any other.
func TestAnExportedVariableCannotCarryAValuePastTheFence(t *testing.T) {
	t.Setenv("RTA_PROFILE_STAGING_NAMESPACE", "prod")
	out, errOut, err := runWith(t, fenceCLIRegistry(t), fencedCLI, "app", "status", "--profile", "staging")
	if err == nil || !strings.Contains(errOut, "--namespace is outside what profile") {
		t.Fatalf("an environment variable went past the fence: %v\n%s\n%s", err, out, errOut)
	}
}
