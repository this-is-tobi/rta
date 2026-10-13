package profile

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// clusterRegistry is a plugin shaped like kube: four reads that take a
// `namespace` as their scope, a write that provisions in one (where it is a
// plain input a profile may fill, with the identity it works on as its scope),
// the cluster's own `context` that every capability reads from a profile, and a
// `bucket` that every capability takes as its scope.
func clusterRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	ns := plugin.Field{Name: "namespace", Type: plugin.String, Config: "namespace"}
	context := plugin.Field{Name: "context", Type: plugin.String, Config: "context", Local: true}
	read := func(id string) plugin.Capability {
		return plugin.Capability{ID: id, Summary: id, Safety: plugin.Read, Run: run, Scope: "namespace",
			Inputs: []plugin.Field{ns, context}}
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "cluster", Summary: "cluster", Capabilities: []plugin.Capability{
		read("cluster.pod.list"), read("cluster.event.list"), read("cluster.deploy.list"), read("cluster.pvc.list"),
		{ID: "cluster.sa.provision", Summary: "provision", Safety: plugin.Write, Run: run, Scope: "name",
			Inputs: []plugin.Field{
				{Name: "name", Type: plugin.String, Positional: true, Required: true},
				{Name: "namespace", Type: plugin.String, Config: "namespace", Local: true}, context}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(plugin.Plugin{Name: "store", Summary: "store", Capabilities: []plugin.Capability{
		{ID: "store.object.get", Summary: "get", Safety: plugin.Read, Run: run, Scope: "bucket",
			Inputs: []plugin.Field{{Name: "bucket", Type: plugin.String, Config: "bucket"}}},
		{ID: "store.object.list", Summary: "list", Safety: plugin.Read, Run: run, Scope: "bucket",
			Inputs: []plugin.Field{{Name: "bucket", Type: plugin.String, Config: "bucket"}}},
		{ID: "store.object.one", Summary: "one", Safety: plugin.Read, Run: run, Scope: "bucket",
			Inputs: []plugin.Field{{Name: "bucket", Type: plugin.String, Config: "bucket"},
				{Name: "out", Type: plugin.Path, Config: "out", Local: true}}},
	}}); err != nil {
		t.Fatal(err)
	}
	return reg
}

const clusterHead = "profiles:\n  staging:\n    plugins:\n      cluster:\n"

// **A `set:` line that every read ignores is a line somebody believes is in
// effect.** The key passes the check, because something a profile may fill reads
// it, and `profile show` lists it as the namespace the environment works in while
// every pod, event and deployment is read from the context's own. Said as a note:
// the profile still resolves — a file with such a line in it is an operator's
// existing one, and an invalid profile refuses every call — but nothing about it
// reads as ok any more.
func TestASetLineEveryReadIgnoresIsNotedAndTheProfileStillResolves(t *testing.T) {
	reg := clusterRegistry(t)
	cfg := load(t, clusterHead+"        set:\n          context: staging\n          namespace: apps\n")

	if problems := Check(cfg, reg); len(problems) != 0 {
		t.Fatalf("the profile is no longer valid: %s", reasons(problems))
	}
	notes := Notes(cfg, reg)
	if len(notes) != 1 {
		t.Fatalf("Notes = %v, want one", notes)
	}
	n := notes[0]
	if n.Name != "staging" || n.Plugin != "cluster" {
		t.Errorf("the note is about %s/%s, want the entry it is in", n.Name, n.Plugin)
	}
	want := "`set: namespace` does not reach cluster.deploy.list, cluster.event.list, cluster.pod.list and 1 more: " +
		"they take it as their scope, which a call names"
	if n.Reason != want {
		t.Errorf("reason = %q\nwant     %q", n.Reason, want)
	}
	if want := "name namespace on the call; the line fills only cluster.sa.provision"; n.Hint != want {
		t.Errorf("hint = %q, want %q", n.Hint, want)
	}
	// And it resolves: the line is honoured where a profile may fill the key.
	c, _ := reg.Capability("cluster.sa.provision")
	conn, verr := Lookup(cfg, c, "staging", reg)
	if verr != nil {
		t.Fatalf("Lookup refused the profile: %v", verr)
	}
	if got := Bind("staging", conn, c, noEnv)["namespace"]; got != "apps" {
		t.Errorf("the provisioning capability was not given the line's value: %v", got)
	}
	read, _ := reg.Capability("cluster.pod.list")
	if got, filled := Bind("staging", conn, read, noEnv)["namespace"]; filled {
		t.Errorf("a read that takes the key as its scope was given %v", got)
	}
}

// A key every capability that reads it fills, a key nothing else reads, and a
// line with a different key, say nothing: the note is for the one that is read by
// some and ignored by the rest.
func TestNothingIsNotedForALineEveryReaderTakes(t *testing.T) {
	reg := clusterRegistry(t)
	for name, body := range map[string]string{
		"a key every capability fills": "        set:\n          context: staging\n",
		"no line at all":               "        secrets: {}\n",
	} {
		if notes := Notes(load(t, clusterHead+body), reg); len(notes) != 0 {
			t.Errorf("%s: %v", name, notes)
		}
	}
}

// One capability is one capability: the sentence agrees with its subject.
func TestTheNoteNamesOneCapabilityInTheSingular(t *testing.T) {
	ns := plugin.Field{Name: "namespace", Type: plugin.String, Config: "namespace"}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "cluster", Summary: "cluster", Capabilities: []plugin.Capability{
		{ID: "cluster.pod.list", Summary: "pods", Safety: plugin.Read, Run: run, Scope: "namespace",
			Inputs: []plugin.Field{ns}},
		{ID: "cluster.sa.provision", Summary: "provision", Safety: plugin.Write, Run: run, Scope: "name",
			Inputs: []plugin.Field{{Name: "name", Type: plugin.String, Positional: true},
				{Name: "namespace", Type: plugin.String, Config: "namespace", Local: true}}},
		{ID: "cluster.sa.revoke", Summary: "revoke", Safety: plugin.Write, Run: run, Scope: "name",
			Inputs: []plugin.Field{{Name: "name", Type: plugin.String, Positional: true},
				{Name: "namespace", Type: plugin.String, Config: "namespace", Local: true}}},
	}}); err != nil {
		t.Fatal(err)
	}
	notes := Notes(load(t, clusterHead+"        set:\n          namespace: apps\n"), reg)
	if len(notes) != 1 || notes[0].Reason != "`set: namespace` does not reach cluster.pod.list: "+
		"it takes it as its scope, which a call names" ||
		notes[0].Hint != "name namespace on the call; the line fills only cluster.sa.provision, cluster.sa.revoke" {
		t.Errorf("Notes = %+v", notes)
	}
}

// Where every capability that reads a key takes it as its scope there is nothing
// the line could do, and that was already a refusal — in the words for a path,
// which a scope is not. The same sentence for both blocks, saying what is true.
func TestAKeyEveryReaderTakesAsItsScopeIsRefusedInItsOwnWords(t *testing.T) {
	reg := clusterRegistry(t)
	for block, body := range map[string]string{
		"set":  "      store:\n        set:\n          bucket: logs\n",
		"only": "      store:\n        only:\n          bucket: [logs]\n",
	} {
		cfg := load(t, "profiles:\n  staging:\n    plugins:\n"+body)
		problems := Check(cfg, reg)
		if len(problems) != 1 {
			t.Fatalf("%s: Check = %s", block, reasons(problems))
		}
		p := problems[0]
		if !strings.Contains(p.Reason, `"bucket" is a config key in store, but a profile may not fill it`) {
			t.Errorf("%s: reason = %q", block, p.Reason)
		}
		if want := "store.object.get, store.object.list, store.object.one take it as their scope, which a call names — a profile never supplies one"; p.Hint != want {
			t.Errorf("%s: hint = %q, want %q", block, p.Hint, want)
		}
	}
	// A path is refused for the reason a path is, and says so.
	cfg := load(t, "profiles:\n  staging:\n    plugins:\n      store:\n        set:\n          out: /tmp/x\n")
	problems := Check(cfg, reg)
	if len(problems) != 1 || !strings.Contains(problems[0].Hint, "never what it reads or where it writes") {
		t.Errorf("a path is no longer refused in its own words: %s", reasons(problems))
	}
}

// The note rides on the plugin's own declaration, so a key that stops being a
// scope stops being noted, and nothing here names a plugin.
func TestTheNoteDependsOnTheDeclarationAlone(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "cluster", Summary: "cluster", Capabilities: []plugin.Capability{
		{ID: "cluster.pod.list", Summary: "pods", Safety: plugin.Read, Run: run,
			Inputs: []plugin.Field{{Name: "namespace", Type: plugin.String, Config: "namespace"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	cfg := load(t, clusterHead+"        set:\n          namespace: apps\n")
	if notes := Notes(cfg, reg); len(notes) != 0 {
		t.Errorf("a namespace that is no one's scope was noted: %v", notes)
	}
	if got := Bind("staging", cfg.Profiles["staging"].Plugins["cluster"], mustCap(t, reg, "cluster.pod.list"), noEnv)["namespace"]; got != "apps" {
		t.Errorf("an ordinary input was not filled from the profile: %v", got)
	}
}

func mustCap(t *testing.T, reg *registry.Registry, id string) plugin.Capability {
	t.Helper()
	c, ok := reg.Capability(id)
	if !ok {
		t.Fatalf("no %s", id)
	}
	return c
}
