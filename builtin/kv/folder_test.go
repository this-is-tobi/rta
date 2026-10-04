package kv

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func setKeys(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		text(t, runSet, map[string]any{"key": k, "value": "v"}, false)
	}
}

func notFoundHint(t *testing.T, sf plugin.Surface, key string) string {
	t.Helper()
	_, err := runGet(context.Background(), req(map[string]any{"key": key}, false).WithSurface(sf))
	ve := view.AsError(err, "x")
	if ve.Code != "kv.notfound" {
		t.Fatalf("%q: want kv.notfound, got %+v", key, ve)
	}
	return ve.Hint
}

// `kv tree` draws folders, so a folder's name is a natural thing to type into
// kv get; the refusal says what it is and which keys are in it, instead of
// sending a person to a listing of everything.
func TestAFolderNameIsRecognisedAndItsKeysNamed(t *testing.T) {
	setup(t)
	setKeys(t, "db/password", "db/user", "db-replica", "api-token")

	for _, key := range []string{"db", "db/"} {
		if got, want := notFoundHint(t, plugin.SurfaceCLI, key), "db/ is a folder: db/password, db/user"; got != want {
			t.Errorf("%q: hint = %q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"d", "db-", "nothing"} {
		if got := notFoundHint(t, plugin.SurfaceCLI, key); got != "`rta kv list` lists every key" {
			t.Errorf("%q is no folder, hint = %q", key, got)
		}
	}
}

// A folder of many keys names the first few and the listing that names the
// rest, in the caller's own terms.
func TestABigFolderNamesTheListingForTheRest(t *testing.T) {
	setup(t)
	setKeys(t, "env/a", "env/b", "env/c", "env/d", "env/e", "env/f")

	if got, want := notFoundHint(t, plugin.SurfaceCLI, "env"),
		"env/ is a folder of 6 keys: env/a, env/b, env/c, env/d and 2 more — `rta kv list env/` lists them"; got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}
	got := notFoundHint(t, plugin.SurfaceMCP, "env")
	if !strings.Contains(got, "`kv_list {\"folder\":\"env/\"}` lists them") || strings.Contains(got, "rta ") {
		t.Errorf("over MCP the hint = %q, want the tool call and no command line", got)
	}
}

// Naming the keys in a folder reveals nothing kv.list does not already hand
// every caller, and the refusal still refuses: a folder is not a key, so no
// value comes back for it.
func TestAFolderNameStillRefusesAndRevealsNothing(t *testing.T) {
	setup(t)
	setKeys(t, "db/password")

	for name, run := range map[string]plugin.Handler{"get": runGet, "show": runShow, "env": runEnv} {
		v, err := run(context.Background(), req(map[string]any{"key": "db"}, false))
		if err == nil {
			t.Errorf("%s: a folder name was answered with %v", name, v)
			continue
		}
		if ve := view.AsError(err, "x"); ve.Code != "kv.notfound" || strings.Contains(ve.Message, "password") {
			t.Errorf("%s: %+v", name, ve)
		}
	}
}

// kv list takes a folder, spelled with or without its slash, and shows only the
// keys under it at any depth.
func TestListFiltersByFolder(t *testing.T) {
	setup(t)
	setKeys(t, "db/password", "db/user", "db/replica/user", "db-replica", "api-token")

	names := func(values map[string]any) []string {
		var out []string
		for _, row := range table(t, runList, values).Rows {
			out = append(out, row[0])
		}
		return out
	}
	for _, folder := range []string{"db", "db/"} {
		got := strings.Join(names(map[string]any{"folder": folder}), " ")
		if want := "db/password db/replica/user db/user"; got != want {
			t.Errorf("folder %q lists %q, want %q", folder, got, want)
		}
	}
	if got := strings.Join(names(map[string]any{"folder": "db/replica/"}), " "); got != "db/replica/user" {
		t.Errorf("a nested folder lists %q", got)
	}
	if got := len(names(nil)); got != 5 {
		t.Errorf("no folder lists %d keys, want all 5", got)
	}
	if got := strings.Join(names(map[string]any{"folder": "db/", "match": "user"}), " "); got != "db/replica/user db/user" {
		t.Errorf("a folder and a match together list %q", got)
	}

	tbl := table(t, runList, map[string]any{"folder": "nope/"})
	if len(tbl.Rows) != 0 || tbl.Empty != "No key in nope/. The store holds 5 keys — `rta kv list` shows every one." {
		t.Errorf("an unknown folder: rows %v, empty %q", tbl.Rows, tbl.Empty)
	}
}

// Completion offers the folders the store has, once it opens without asking.
func TestFoldersAreSuggested(t *testing.T) {
	setup(t)
	setKeys(t, "db/password", "db/replica/user", "api-token", "staging/db/password")

	got := strings.Join(suggestFolders(context.Background(), req(nil, false)), " ")
	if want := "db/ db/replica/ staging/ staging/db/"; got != want {
		t.Errorf("folders = %q, want %q", got, want)
	}
}
