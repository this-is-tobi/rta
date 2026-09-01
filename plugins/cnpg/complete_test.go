package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// servesWithArgs is serves plus a record of what kubectl was asked, so a
// completion's argv can be checked as well as its parse.
func servesWithArgs(t *testing.T, doc any) func() string {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	body := filepath.Join(dir, "body.json")
	argv := filepath.Join(dir, "argv.txt")
	if err := os.WriteFile(body, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	fakeKubectl(t, "echo \"$@\" > "+argv+"\ncat "+body+"\n")
	return func() string {
		got, _ := os.ReadFile(argv)
		return string(got)
	}
}

func TestContextCompletionReadsTheKubeconfigAndNothingElse(t *testing.T) {
	argvOf := t.TempDir() + "/argv.txt"
	fakeKubectl(t, "echo \"$@\" > "+argvOf+"\nprintf 'homelab\\nstaging\\n'\n")

	got := suggestContexts(context.Background(), req(nil))
	if !slices.Equal(got, []string{"homelab", "staging"}) {
		t.Errorf("contexts = %v", got)
	}
	argv, _ := os.ReadFile(argvOf)
	if !strings.Contains(string(argv), "config get-contexts") {
		t.Errorf("argv = %q, want a local `config get-contexts` and no cluster call", argv)
	}
}

func TestNamespaceCompletionStripsTheResourcePrefix(t *testing.T) {
	fakeKubectl(t, "printf 'namespace/default\\nnamespace/gitea\\n'\n")
	got := suggestNamespaces(context.Background(), req(map[string]any{"context": "homelab"}))
	if !slices.Equal(got, []string{"default", "gitea"}) {
		t.Errorf("namespaces = %v, want the bare names", got)
	}
}

// With no namespace chosen the cluster completion sweeps every namespace —
// a CNPG cluster conventionally lives in its own, so the default namespace
// would answer nothing on exactly the setups this plugin is for — and each
// entry's description carries the flag the caller still has to pass.
func TestClusterCompletionSweepsEveryNamespaceUntilOneIsChosen(t *testing.T) {
	list := clusterList{Items: []cluster{healthy()}}
	argv := servesWithArgs(t, list)
	got := suggestClusters(context.Background(), req(nil))
	if !slices.Equal(got, []string{"app-db\t--namespace=databases"}) {
		t.Errorf("clusters = %v, want the name with its namespace as the description", got)
	}
	if !strings.Contains(argv(), "--all-namespaces") {
		t.Errorf("argv = %q, want --all-namespaces when none was chosen", argv())
	}

	argv = servesWithArgs(t, list)
	got = suggestClusters(context.Background(), req(map[string]any{"namespace": "databases"}))
	if !slices.Equal(got, []string{"app-db"}) {
		t.Errorf("clusters = %v, want the bare name once the namespace is fixed", got)
	}
	if a := argv(); !strings.Contains(a, "--namespace=databases") || strings.Contains(a, "--all-namespaces") {
		t.Errorf("argv = %q, want the chosen namespace and not a sweep", a)
	}
}

// A value that would be read as a kubectl flag must not reach a completion's
// argv either — the same refusal every Run applies, held on the quiet path.
func TestCompletionRefusesAValueThatWouldBeReadAsAFlag(t *testing.T) {
	fakeKubectl(t, "echo should-not-run >&2\nexit 1\n")
	if got := suggestNamespaces(context.Background(),
		req(map[string]any{"context": "--kubeconfig=/tmp/mine"})); got != nil {
		t.Errorf("a flag-shaped context still answered: %v", got)
	}
	if got := suggestClusters(context.Background(),
		req(map[string]any{"namespace": "--all-namespaces"})); got != nil {
		t.Errorf("a flag-shaped namespace still answered: %v", got)
	}
}

// The Live split, pinned the way plugins/kube pins it: a Suggest that reads
// the cluster answers a deliberate press only, one that reads a local file
// stays on the keystroke channel.
func TestClusterReadingSuggestsAreLiveAndLocalOnesAreNot(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		for _, f := range c.Inputs {
			if f.Suggest == nil {
				continue
			}
			switch f.Name {
			case "namespace", "name":
				if !f.Live {
					t.Errorf("%s: the %s Suggest reads the cluster and must be Live", c.ID, f.Name)
				}
			case "context":
				if f.Live {
					t.Errorf("%s: the context Suggest reads a local file and must not be Live", c.ID)
				}
			}
		}
	}
}
