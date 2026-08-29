package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// A mount shaped the way a real one is: folders inside folders, with the
// secrets an operator is actually looking for two and three levels down.
var mountFixture = map[string]string{
	"/v1/secret/metadata":              `{"data":{"keys":["apps/","shared/","root-token"]}}`,
	"/v1/secret/metadata/apps":         `{"data":{"keys":["billing/","web/"]}}`,
	"/v1/secret/metadata/apps/billing": `{"data":{"keys":["db","stripe"]}}`,
	"/v1/secret/metadata/apps/web":     `{"data":{"keys":["session-key"]}}`,
	"/v1/secret/metadata/shared":       `{"data":{"keys":["smtp"]}}`,
}

func treeOf(t *testing.T, srv *httptest.Server, values map[string]any) view.Tree {
	t.Helper()
	values["address"] = srv.URL
	values["token"] = "t"
	v, err := runKVTree(context.Background(), req(t, "vault.kv.tree", values))
	if err != nil {
		t.Fatal(err)
	}
	tree, ok := v.(view.Tree)
	if !ok {
		t.Fatalf("want Tree, got %s", view.TypeOf(v))
	}
	return tree
}

// paths flattens a tree back into the full paths it drew, which is what a
// person reads off it.
func paths(nodes []view.Node, prefix string) []string {
	var out []string
	for _, n := range nodes {
		full := prefix + n.Label
		if len(n.Children) > 0 {
			out = append(out, paths(n.Children, full)...)
			continue
		}
		out = append(out, full)
	}
	return out
}

func TestTheTreeFindsSecretsSeveralLevelsDown(t *testing.T) {
	srv, asked := recordingVault(t, mountFixture)
	tree := treeOf(t, srv, map[string]any{})

	got := strings.Join(paths(tree.Roots[0].Children, ""), " ")
	for _, want := range []string{
		"apps/billing/db", "apps/billing/stripe", "apps/web/session-key",
		"shared/smtp", "root-token",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not in the tree: %s", want, got)
		}
	}
	// The count is the part somebody reads first.
	if d := tree.Roots[0].Detail; !strings.Contains(d, "5 secrets") {
		t.Errorf("detail = %q, want the secret count", d)
	}
	// Names only, and only the verb that returns them.
	readOnly(t, asked())
}

// --path starts the walk somewhere, which is how a big mount stays readable.
func TestTheTreeCanStartInsideTheMount(t *testing.T) {
	srv, _ := recordingVault(t, mountFixture)
	tree := treeOf(t, srv, map[string]any{"path": "apps/billing"})

	if got := tree.Roots[0].Label; got != "secret/apps/billing" {
		t.Errorf("root = %q, want the path it was asked about", got)
	}
	got := paths(tree.Roots[0].Children, "")
	if len(got) != 2 || got[0] != "db" || got[1] != "stripe" {
		t.Errorf("children = %v, want the two secrets under it", got)
	}
}

// --depth stops the walk, and a folder left unexpanded says so. A folder
// rendered as a leaf would read as an empty one.
func TestAFolderLeftUnexpandedSaysSo(t *testing.T) {
	srv, _ := recordingVault(t, mountFixture)
	tree := treeOf(t, srv, map[string]any{"depth": 1})

	var folders int
	for _, n := range tree.Roots[0].Children {
		if !strings.HasSuffix(n.Label, "/") {
			continue
		}
		folders++
		if !strings.Contains(n.Detail, "--depth") {
			t.Errorf("%s: detail = %q, want it to say why it is empty", n.Label, n.Detail)
		}
		if len(n.Children) > 0 {
			t.Errorf("%s was expanded past --depth 1", n.Label)
		}
	}
	if folders != 2 {
		t.Fatalf("folders = %d, want apps/ and shared/", folders)
	}
}

// **The property that makes this usable in a shared Vault.** A token whose
// policy covers part of a mount is the ordinary case, and ending the walk at
// the first folder it may not list would turn "here is the half you can see"
// into nothing at all.
func TestAForbiddenFolderIsMarkedAndSteppedOver(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimSuffix(r.URL.Path, "/") {
		case "/v1/secret/metadata":
			_, _ = w.Write([]byte(`{"data":{"keys":["open/","locked/"]}}`))
		case "/v1/secret/metadata/open":
			_, _ = w.Write([]byte(`{"data":{"keys":["visible"]}}`))
		default:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
		}
	}))
	defer srv.Close()

	tree := treeOf(t, srv, map[string]any{})
	byName := map[string]view.Node{}
	for _, n := range tree.Roots[0].Children {
		byName[n.Label] = n
	}
	if got := paths(byName["open/"].Children, ""); len(got) != 1 || got[0] != "visible" {
		t.Errorf("the readable half was lost: %v", got)
	}
	if d := byName["locked/"].Detail; d == "" {
		t.Error("a folder the token may not list is drawn as an empty one")
	} else if !strings.Contains(strings.ToLower(d), "denied") {
		t.Errorf("locked/ detail = %q, want the classified reason", d)
	}
}

// A mount with more paths than anybody can read comes back bounded, and says
// it stopped rather than looking complete. Silence here is the failure that
// matters: a tree missing half a mount reads exactly like a mount with half
// as much in it.
func TestAHugeMountIsBoundedAndSaysSo(t *testing.T) {
	var keys []string
	for i := range maxTreeNodes + 50 {
		keys = append(keys, `"secret-`+itoa(i)+`"`)
	}
	body := `{"data":{"keys":[` + strings.Join(keys, ",") + `]}}`
	srv, _ := recordingVault(t, map[string]string{"/v1/secret/metadata": body})

	tree := treeOf(t, srv, map[string]any{})
	if d := tree.Roots[0].Detail; !strings.Contains(d, "stopped") {
		t.Errorf("detail = %q, want it to say the walk was cut short", d)
	}
	if n := len(tree.Roots[0].Children); n > maxTreeNodes+1 {
		t.Errorf("children = %d, want the walk bounded at %d", n, maxTreeNodes)
	}
}

// An unreachable or forbidden *root* is the one listing whose failure is the
// answer: there is nothing to draw.
func TestAWalkThatCannotStartIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
	}))
	defer srv.Close()

	_, err := runKVTree(context.Background(), req(t, "vault.kv.tree",
		map[string]any{"address": srv.URL, "token": "t"}))
	if err == nil {
		t.Fatal("a mount the token cannot list produced a tree")
	}
	if ve, ok := err.(*view.Error); !ok || ve.Code != "vault.denied" {
		t.Fatalf("err = %v, want vault.denied", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
