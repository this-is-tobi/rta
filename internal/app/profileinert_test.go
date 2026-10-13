package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// clusterishRegistry is shaped like kube: reads that take a namespace as their
// scope, and one capability where it is an input a profile may fill.
func clusterishRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	say := func(_ context.Context, req plugin.Request) (view.View, error) {
		return view.Text{Body: fmt.Sprintf("ran namespace=%q", req.String("namespace"))}, nil
	}
	read := func(id string) plugin.Capability {
		return plugin.Capability{ID: id, Summary: id, Safety: plugin.Read, Run: say, Scope: "namespace",
			Inputs: []plugin.Field{
				{Name: "namespace", Type: plugin.String, Config: "namespace", Help: "namespace"},
				{Name: "context", Type: plugin.String, Config: "context", Local: true, Help: "context"},
			}}
	}
	if err := reg.Register(plugin.Plugin{Name: "cluster", Summary: "cluster", Capabilities: []plugin.Capability{
		read("cluster.pod.list"), read("cluster.event.list"),
		{ID: "cluster.sa.provision", Summary: "provision", Safety: plugin.Write, Run: say, Scope: "name",
			Inputs: []plugin.Field{
				{Name: "name", Type: plugin.String, Positional: true, Required: true, Help: "name"},
				{Name: "namespace", Type: plugin.String, Config: "namespace", Local: true, Help: "namespace"},
			}},
	}}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// **A `set:` line that a read ignores is said wherever the line is read back.**
// `profile set` wrote it without a word, `profile show` and the list called it
// the environment's namespace, `config check` found nothing, and every pod,
// deployment and event was read from the context's own namespace — an answer
// that looks fine and is about the wrong place. Nothing about the profile is
// refused, since the line is honoured where a profile may fill the key and an
// invalid profile refuses every call; every surface now says which capabilities
// take the key as their scope, and what to do.
func TestASetLineEveryReadIgnoresIsSaidWhereverItIsReadBack(t *testing.T) {
	run := session(t, clusterishRegistry(t))

	out, errOut, err := run("profile", "set", "staging", "--plugin", "cluster", "--set", "namespace=apps")
	if err != nil {
		t.Fatalf("the profile was refused, and it must still be written: %v\n%s", err, errOut)
	}
	const reason = "`set: namespace` does not reach cluster.event.list, cluster.pod.list: they take it as their scope, which a call names"
	const hint = "name namespace on the call; the line fills only cluster.sa.provision"
	if !strings.Contains(out, "warning") || !strings.Contains(out, reason) || !strings.Contains(out, hint) {
		t.Errorf("profile set did not say the line is ignored by the reads:\n%s", out)
	}

	show, _, err := run("profile", "show", "staging")
	if err != nil || !strings.Contains(show, reason) {
		t.Errorf("profile show: %v\n%s", err, show)
	}
	list, _, err := run("profile", "list")
	if err != nil || !strings.Contains(list, "warn") || !strings.Contains(list, "`set: namespace` does not reach") {
		t.Errorf("profile list: %v\n%s", err, list)
	}
	doctor, _, _ := run("doctor")
	if !strings.Contains(doctor, "`set: namespace` does not reach cluster.event.list") {
		t.Errorf("doctor does not say it:\n%s", doctor)
	}
	check, checkErr, err := run("config", "check")
	if err == nil || !strings.Contains(checkErr, "core.config.check") ||
		!strings.Contains(check, "`set: namespace` does not reach cluster.event.list") {
		t.Errorf("config check passed a line the reads ignore: %v\n%s\n%s", err, check, checkErr)
	}

	// The profile is not refused, and the line does what it said it did not: a
	// read is not given the namespace, so the person is told to name it.
	got, errOut, err := run("cluster", "pod", "list", "--profile", "staging")
	if err != nil || !strings.Contains(got, `ran namespace=""`) {
		t.Errorf("the profile no longer resolves: %v\n%s\n%s", err, got, errOut)
	}
	got, _, err = run("cluster", "pod", "list", "--profile", "staging", "--namespace", "apps")
	if err != nil || !strings.Contains(got, `ran namespace="apps"`) {
		t.Errorf("naming the namespace on the call: %v\n%s", err, got)
	}
}

// A line every capability takes says nothing, in any of the surfaces.
func TestASetLineEveryReaderTakesIsSaidNowhere(t *testing.T) {
	run := session(t, clusterishRegistry(t))
	if _, errOut, err := run("profile", "set", "staging", "--plugin", "cluster", "--set", "context=staging"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	for _, args := range [][]string{{"profile", "show", "staging"}, {"profile", "list"}, {"config", "check"}} {
		out, errOut, err := run(args...)
		if err != nil || strings.Contains(out+errOut, "does not reach") || strings.Contains(out, "warn") {
			t.Errorf("%v said something about a line every reader takes: %v\n%s\n%s", args, err, out, errOut)
		}
	}
}
