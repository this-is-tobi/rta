package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A connection can be stated on the command line of one call: the forward is
// raised for it and closed after, a credential is a reference, and nothing is
// written. These hold what that is allowed to be, which is a person's act at a
// terminal and nothing an agent can name.

// whoamiRegistry is connRegistry with a handler that says how it was reached.
func whoamiRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := connRegistry(t)
	c, _ := reg.Capability("db.status")
	c.ID, c.Run = "db.whoami", func(_ context.Context, req plugin.Request) (view.View, error) {
		return view.Text{Body: fmt.Sprintf("profile=%q tunnel=%q host=%q", req.Profile(), req.Tunnel(), req.String("host"))}, nil
	}
	out := registry.New()
	if err := out.Register(plugin.Plugin{Name: "db", Summary: "db plugin", Capabilities: []plugin.Capability{c}}); err != nil {
		t.Fatal(err)
	}
	return out
}

// kubectlForwarding puts a kubectl of this test's own first on PATH that
// reports a forward to a listener: no cluster and no kubeconfig is read.
func kubectlForwarding(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	bin := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\necho 'Forwarding from 127.0.0.1:%d -> 5432'\nwhile true; do sleep 1; done\n",
		ln.Addr().(*net.TCPAddr).Port)
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestAServiceNamedOnTheCommandLineIsReachedThroughAForwardForThatCall(t *testing.T) {
	kubectlForwarding(t)
	out, errOut, err := runWith(t, whoamiRegistry(t), "output: pretty\n", "db", "whoami", "--kube", "homelab/databases/svc/postgres:5432")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(out, `profile="ad hoc"`) || !strings.Contains(out, `tunnel="kube"`) || !strings.Contains(out, `host="127.0.0.1"`) {
		t.Errorf("the handler was told %q, want a call through a kube forward under the name ad hoc", strings.TrimSpace(out))
	}
}

// Nothing is written: not a profile, not a line of config, so there is nothing
// to clean up and nothing a later command could be reaching by accident.
func TestAConnectionOnTheCommandLineWritesNothing(t *testing.T) {
	kubectlForwarding(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, errOut, err := runConfigAt(t, whoamiRegistry(t), path, "db", "whoami",
		"--kube", "homelab/databases/svc/postgres:5432"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a config file was written")
	}
}

func TestACredentialIsAReferenceAndNeverTheCredential(t *testing.T) {
	kubectlForwarding(t)
	_, errOut, err := runWith(t, whoamiRegistry(t), "", "db", "whoami",
		"--kube", "homelab/databases/svc/postgres:5432", "--secret", "password=hunter2")
	if err == nil || !strings.Contains(errOut, "core.profile.secret.value") || strings.Contains(errOut, "hunter2") {
		t.Errorf("a credential on the command line was taken, or echoed: %v\n%s", err, errOut)
	}
}

func TestAClusterCredentialSaysWhichClusterHoldsIt(t *testing.T) {
	_, errOut, err := runWith(t, whoamiRegistry(t), "", "db", "whoami", "--secret", "password=kube:postgres-app/password")
	if err == nil || !strings.Contains(errOut, "core.adhoc.secrets") || !strings.Contains(errOut, "does not say which") {
		t.Errorf("a kube: reference with no cluster was taken: %v\n%s", err, errOut)
	}
}

func TestTheFlagsAreRefusedBesideAProfileAndAreNeverRanked(t *testing.T) {
	_, errOut, err := runWith(t, whoamiRegistry(t), "profiles:\n  homelab:\n    plugins:\n      db:\n        set: {host: db.internal}\n",
		"db", "whoami", "--profile", "homelab", "--kube", "homelab/databases/svc/postgres:5432")
	if err == nil || !strings.Contains(errOut, "core.adhoc.both") {
		t.Errorf("a profile and a coordinate were both taken: %v\n%s", err, errOut)
	}
}

func TestACoordinateThatDoesNotParseIsRefusedBeforeAnythingIsOpened(t *testing.T) {
	_, errOut, err := runWith(t, whoamiRegistry(t), "", "db", "whoami", "--kube", "not-a-coordinate")
	if err == nil || !strings.Contains(errOut, "core.adhoc.tunnel") {
		t.Errorf("a bad coordinate was taken: %v\n%s", err, errOut)
	}
	// And both ways to reach a cluster's Secrets are not both stated.
	_, errOut, err = runWith(t, whoamiRegistry(t), "", "db", "whoami",
		"--kube", "homelab/databases/svc/postgres:5432", "--secrets-from", "homelab/databases")
	if err == nil || !strings.Contains(errOut, "core.adhoc.tunnel") {
		t.Errorf("two answers for where the Secrets are were taken: %v\n%s", err, errOut)
	}
}

// What the hints of a call would paste must not reach somewhere else: the name
// of the connection is not one a profile can have, so a hint that carries it
// fails where it is pasted.
func TestTheNameOfAnAdHocConnectionIsNotAProfileReference(t *testing.T) {
	kubectlForwarding(t)
	if config.ValidRef("ad hoc") {
		t.Fatal("ad hoc is a valid profile reference, and a hint carrying it would reach a profile of that name")
	}
}

// The flags exist on the command line of a connection-bearing capability, and
// on the command line of none that has no connection to state.
func TestTheConnectionFlagsAreOnlyOnACapabilityThatHasAConnection(t *testing.T) {
	root := NewRoot(whoamiRegistry(t), "test")
	cmd, _, err := root.Find([]string{"db", "whoami"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kube", "secret", "secrets-from"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("db whoami has no --%s", name)
		}
	}
	// Not on a capability with no address a coordinate could replace: the audit
	// of a cluster reaches it through the kubeconfig, and a gen or a sys call
	// reaches nothing.
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"sys", "cpu"}, {"gen", "password"}, {"audit", "kube", "eol"}, {"audit", "kube", "netpol"}} {
		other, _, err := NewRoot(reg, "test").Find(args)
		if err != nil || other == nil || other.Name() == "rta" {
			t.Errorf("%v is not a command: %v", args, err)
			continue
		}
		for _, name := range []string{"kube", "secret", "secrets-from"} {
			if other.Flags().Lookup(name) != nil {
				t.Errorf("%v has a --%s it cannot use", args, name)
			}
		}
	}
}

// The line above a result names where the connection typed on its command line
// goes, from the flags as typed — before any check has run, so a connection
// about to be refused still says where it was going.
func TestTheLineAboveAResultSaysWhereTheFlagsSendTheCall(t *testing.T) {
	for name, tc := range map[string]struct {
		set  map[string]string
		want string
	}{
		"nothing typed":         {nil, ""},
		"a coordinate":          {map[string]string{"kube": "homelab/databases/svc/postgres:5432"}, "through homelab/databases/svc/postgres:5432"},
		"the cluster of Secret": {map[string]string{"secrets-from": "homelab/databases", "secret": "password=kube:pg/password"}, "credentials from homelab/databases"},
		"a reference only":      {map[string]string{"secret": "password=kv:entry"}, "credentials by reference"},
	} {
		cmd := &cobra.Command{Use: "whoami"}
		cmd.Flags().String("kube", "", "")
		cmd.Flags().String("secrets-from", "", "")
		cmd.Flags().StringArray("secret", nil, "")
		for k, v := range tc.set {
			if err := cmd.Flags().Set(k, v); err != nil {
				t.Fatal(err)
			}
		}
		got, stated := adHocWords(cmd)
		if got != tc.want || stated != (tc.want != "") {
			t.Errorf("%s: %q (stated %v), want %q", name, got, stated, tc.want)
		}
	}
}
