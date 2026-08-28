package recent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

func isolated(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

// cap builds a capability over the given fields.
func capWith(id string, fields ...plugin.Field) plugin.Capability {
	return plugin.Capability{ID: id, Summary: "s", Safety: plugin.Read, Inputs: fields}
}

func str(name string) plugin.Field  { return plugin.Field{Name: name, Type: plugin.String} }
func path(name string) plugin.Field { return plugin.Field{Name: name, Type: plugin.Path} }

// What somebody used comes back, most recent first.
func TestWhatWasUsedComesBack(t *testing.T) {
	isolated(t)
	c := capWith("s3.object.list", str("bucket"))
	Record(plugin.SurfaceCLI, c, map[string]any{"bucket": "first"})
	Record(plugin.SurfaceCLI, c, map[string]any{"bucket": "second"})
	Record(plugin.SurfaceCLI, c, map[string]any{"bucket": "first"})

	got := Load().For("s3.object.list", "bucket")
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Errorf("shortlist = %v, want the most recent first with no duplicate", got)
	}
}

// A shortlist belongs to the plugin, not to one capability: `s3 object list
// --bucket` and `s3 object get --bucket` name the same bucket.
func TestAShortlistIsSharedAcrossOnePluginsCapabilities(t *testing.T) {
	isolated(t)
	Record(plugin.SurfaceCLI, capWith("s3.object.list", str("bucket")),
		map[string]any{"bucket": "mine"})

	if got := Load().For("s3.object.get", "bucket"); len(got) != 1 || got[0] != "mine" {
		t.Errorf("s3.object.get sees %v, want the bucket the listing used", got)
	}
	if got := Load().For("pg.table.list", "bucket"); len(got) != 0 {
		t.Errorf("another plugin sees %v, want nothing", got)
	}
}

// A credential never reaches the file. This is the property the whole design
// hangs on: a convenience that writes to disk must not be the thing that
// writes somebody's password to disk.
func TestACredentialIsNeverRecorded(t *testing.T) {
	isolated(t)
	c := capWith("vault.kv.get",
		str("path"),
		plugin.Field{Name: "token", Type: plugin.Secret, Local: true},
		plugin.Field{Name: "body", Type: plugin.Text},
		plugin.Field{Name: "force", Type: plugin.Bool},
	)
	Record(plugin.SurfaceCLI, c, map[string]any{
		"path":  "secret/data/app",
		"token": "hvs.CAESIJ-not-a-real-token",
		"body":  "a long body somebody wrote in an editor",
		"force": true,
	})

	values := Load()
	if got := values.For("vault.kv.get", "path"); len(got) != 1 {
		t.Fatalf("the path was not recorded: %v", got)
	}
	for _, name := range []string{"token", "body", "force"} {
		if got := values.For("vault.kv.get", name); len(got) != 0 {
			t.Errorf("%s was recorded as %v", name, got)
		}
	}
	// And not anywhere else in the file either, whatever the keys say.
	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hvs.CAESIJ") {
		t.Fatalf("the credential is in %s", Path())
	}
}

// An agent's values never become the operator's suggestions. A call an agent
// made choosing what a person is offered next is a quiet way to be steered.
func TestAnAgentNeverWritesTheShortlist(t *testing.T) {
	isolated(t)
	c := capWith("s3.object.list", str("bucket"))
	Record(plugin.SurfaceMCP, c, map[string]any{"bucket": "attacker-chosen"})
	Record(plugin.SurfaceCompletion, c, map[string]any{"bucket": "also-no"})
	Record(plugin.SurfaceUnknown, c, map[string]any{"bucket": "still-no"})

	if got := Load().For("s3.object.list", "bucket"); len(got) != 0 {
		t.Errorf("shortlist = %v, want nothing an agent supplied", got)
	}
	if _, err := os.Stat(Path()); !os.IsNotExist(err) {
		t.Errorf("a file was written for a surface that may not record: %v", err)
	}
}

// The file is only readable by its owner: it names the buckets, databases and
// hosts somebody works with.
func TestTheFileIsPrivate(t *testing.T) {
	isolated(t)
	Record(plugin.SurfaceCLI, capWith("s3.object.list", str("bucket")),
		map[string]any{"bucket": "mine"})

	info, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

// A value that would display as something other than what it is is not
// remembered at all.
//
// Refused rather than cleaned: a completion entry is accepted with one
// keystroke, so offering a scrubbed version would hand somebody a value that
// is not the one that worked.
func TestADeceptiveValueIsNotRemembered(t *testing.T) {
	isolated(t)
	c := capWith("demo.run", str("name"))
	for _, hostile := range []string{
		"we\u202elcome",    // a bidi override: displays in an order it is not stored in
		"clean\x1b[2Kname", // an escape sequence a terminal acts on
		"tag\u200bged",     // a zero-width space
	} {
		Record(plugin.SurfaceCLI, c, map[string]any{"name": hostile})
	}
	if got := Load().For("demo.run", "name"); len(got) != 0 {
		t.Errorf("shortlist = %q, want nothing that reads as one thing and is another", got)
	}
	// An ordinary value alongside them is still kept.
	Record(plugin.SurfaceCLI, c, map[string]any{"name": "ordinary"})
	if got := Load().For("demo.run", "name"); len(got) != 1 || got[0] != "ordinary" {
		t.Errorf("shortlist = %q, want the ordinary value", got)
	}
}

// Nothing enormous is kept: a completion list is one line per entry, and a SQL
// statement is neither completable nor something to write to disk quietly.
func TestAnEnormousValueIsNotKept(t *testing.T) {
	isolated(t)
	c := capWith("pg.query", str("sql"))
	Record(plugin.SurfaceCLI, c, map[string]any{"sql": strings.Repeat("select 1; ", 40)})

	if got := Load().For("pg.query", "sql"); len(got) != 0 {
		t.Errorf("kept %d chars", len(got[0]))
	}
}

// The shortlist stays short, and the file stays bounded.
func TestTheShortlistStaysShort(t *testing.T) {
	isolated(t)
	c := capWith("s3.object.list", str("bucket"))
	for i := range perInput + 5 {
		Record(plugin.SurfaceCLI, c, map[string]any{"bucket": string(rune('a' + i))})
	}
	got := Load().For("s3.object.list", "bucket")
	if len(got) != perInput {
		t.Errorf("shortlist = %d entries, want %d", len(got), perInput)
	}
	if got[0] != string(rune('a'+perInput+4)) {
		t.Errorf("head = %q, want the most recent", got[0])
	}
}

// A path is worth remembering, and it is exactly the case a real bug turned
// on: `net hosts list --file ./container/hosts` is a value somebody types once
// and then needs on every command after it.
func TestAPathIsRemembered(t *testing.T) {
	isolated(t)
	Record(plugin.SurfaceCLI, capWith("net.hosts.list", path("file")),
		map[string]any{"file": "./container/etc/hosts"})

	if got := Load().For("net.hosts.rm", "file"); len(got) != 1 || got[0] != "./container/etc/hosts" {
		t.Errorf("shortlist = %v, want the file the listing used", got)
	}
}

// Each item of a list input is remembered on its own, since each is completed
// on its own.
func TestEachItemOfAListIsRemembered(t *testing.T) {
	isolated(t)
	Record(plugin.SurfaceCLI, capWith("todo.add", plugin.Field{Name: "tag", Type: plugin.StringSlice}),
		map[string]any{"tag": []string{"recipe", "italian"}})

	got := Load().For("todo.add", "tag")
	if len(got) != 2 {
		t.Fatalf("shortlist = %v, want both tags", got)
	}
}

// A closed set is already offered in full; repeating three of its members
// above the list is noise.
func TestAClosedSetIsNotRemembered(t *testing.T) {
	isolated(t)
	Record(plugin.SurfaceCLI, capWith("net.dns",
		plugin.Field{Name: "type", Type: plugin.String, Options: []string{"a", "aaaa", "mx"}}),
		map[string]any{"type": "mx"})

	if got := Load().For("net.dns", "type"); len(got) != 0 {
		t.Errorf("shortlist = %v, want nothing for a closed set", got)
	}
}

// An unreadable or absent store costs a suggestion, never a command.
func TestAnUnreadableStoreIsSilent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "recent.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load().For("s3.object.list", "bucket"); got != nil {
		t.Errorf("shortlist = %v, want nothing", got)
	}
	// And writing over it still works rather than failing the run.
	Record(plugin.SurfaceCLI, capWith("s3.object.list", str("bucket")),
		map[string]any{"bucket": "mine"})
	if got := Load().For("s3.object.list", "bucket"); len(got) != 1 {
		t.Errorf("shortlist = %v after a rewrite", got)
	}
}

// A credential is refused by name too, as a backstop for a declaration that
// got its type wrong — which builtin/http's own `bearer` did.
func TestACredentialShapedNameIsRefused(t *testing.T) {
	isolated(t)
	c := capWith("demo.call",
		str("bearer"), str("api-token"), str("password"), str("auth"),
		plugin.Field{Name: "id", Type: plugin.String, Config: "secret-key"},
		str("bucket"),
	)
	Record(plugin.SurfaceCLI, c, map[string]any{
		"bearer": "ghp_notarealtoken", "api-token": "t", "password": "p", "auth": "a",
		"id": "also-a-credential", "bucket": "mine",
	})
	values := Load()
	for _, name := range []string{"bearer", "api-token", "password", "auth", "id"} {
		if got := values.For("demo.call", name); len(got) != 0 {
			t.Errorf("%s was remembered as %v", name, got)
		}
	}
	if got := values.For("demo.call", "bucket"); len(got) != 1 {
		t.Errorf("an ordinary input was refused as well: %v", got)
	}
}

// An Authorization header is refused whatever the field is called: it is a
// credential on a list of header names that are worth remembering.
func TestAnAuthorizationHeaderIsRefused(t *testing.T) {
	isolated(t)
	c := capWith("http.get", plugin.Field{Name: "header", Type: plugin.StringSlice})
	Record(plugin.SurfaceCLI, c, map[string]any{"header": []string{
		"Accept: application/json",
		"Authorization: Bearer ghp_notarealtoken",
	}})
	got := Load().For("http.get", "header")
	if len(got) != 1 || got[0] != "Accept: application/json" {
		t.Errorf("shortlist = %v, want only the header that is not a credential", got)
	}
}

// A value with a newline or a tab in it is not one completion entry.
func TestAMultiLineValueIsNotRemembered(t *testing.T) {
	isolated(t)
	c := capWith("demo.run", str("name"))
	Record(plugin.SurfaceCLI, c, map[string]any{"name": "prod-backups\nrm -rf ~/.ssh"})
	Record(plugin.SurfaceCLI, c, map[string]any{"name": "prod\tbackups"})
	if got := Load().For("demo.run", "name"); len(got) != 0 {
		t.Errorf("shortlist = %q, want nothing that is not one line", got)
	}
}
