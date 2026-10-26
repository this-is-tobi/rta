package plugindist

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/plugintrust"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The system root's record is read for what it says and nothing else: an
// image's installs show up beside the operator's, and no system root means
// no entries rather than an error.
func TestTheSystemLockIsReadAndNeverRequired(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_SYSTEM_DIR", "")
	if got := ReadSystemLock(); len(got) != 0 {
		t.Fatalf("no system root, yet %d entries", len(got))
	}
	root := t.TempDir()
	t.Setenv("RTA_SYSTEM_DIR", root)
	if err := os.MkdirAll(filepath.Join(root, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `{"plugins":[{"name":"kube","version":"0.3.8","digest":"abc","index":"official"}]}`
	if err := os.WriteFile(SystemLockPath(), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ReadSystemLock()
	if len(got) != 1 || got[0].Name != "kube" || got[0].Index != "official" {
		t.Fatalf("ReadSystemLock() = %+v, want the image's kube", got)
	}
	if len(ReadLock()) != 0 {
		t.Fatal("the operator's record picked up the image's entry")
	}
}

// breakLock writes a record holding entries that does not parse, with the
// trailing comma a hand edit leaves, and returns what it wrote.
func breakLock(t *testing.T, entries ...string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(LockPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	doc := "{\"plugins\":[\n  " + strings.Join(entries, ",\n  ") + ",\n]}\n"
	if err := os.WriteFile(LockPath(), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return doc
}

// refusedOverTheLock checks one refusal is the record's, naming the file, and
// that the file is as it was.
func refusedOverTheLock(t *testing.T, what string, verr *view.Error, doc string) {
	t.Helper()
	if verr == nil || verr.Code != "plugin.lock.unreadable" {
		t.Fatalf("%s over a lock that does not parse: %v, want plugin.lock.unreadable", what, verr)
	}
	if !strings.Contains(verr.Message, LockPath()) {
		t.Errorf("%s: the refusal does not name the file: %v", what, verr)
	}
	raw, err := os.ReadFile(LockPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != doc {
		t.Fatalf("%s rewrote the lock it could not read:\n%s", what, raw)
	}
}

// The record is plain JSON and documented as not sealed, so a hand edit that
// leaves a trailing comma is an ordinary way for it to stop parsing. Reading
// it for display shows nothing then, which is harmless; rewriting it from
// that nothing kept the one plugin being installed and dropped every other,
// after which `rta plugin upgrade pg` answered that pg was not managed by
// rta. An install refuses instead, and before it lands anything: refused only
// when the record was rewritten, it had already placed the artifact, pointed
// bin/ at it and trusted it, which is everything an install does but the one
// line saying so — and the empty reading had also let it past the check that
// the plugin was not installed already.
func TestAnInstallOverALockThatDoesNotParseLandsNothing(t *testing.T) {
	testData(t)
	bin := hello(t)
	attach(t, helloManifest(t, bin, ""))
	doc := breakLock(t, `{"name":"kube","digest":"aaa","index":"official"}`,
		`{"name":"pg","digest":"bbb","index":"official"}`)

	for _, c := range []struct {
		what    string
		install func(context.Context, string, io.Writer) (Report, *view.Error)
	}{{"an install", Install}, {"a dry run", PreviewInstall}} {
		_, verr := c.install(context.Background(), "hello", io.Discard)
		refusedOverTheLock(t, c.what, verr, doc)
	}
	assertNothingInstalled(t, sha256Of(t, bin))
	if _, err := os.Lstat(filepath.Join(BinDir(), pluginhost.BinaryName("hello"))); err == nil {
		t.Error("bin/ links a plugin the refused install was never recorded as installing")
	}
}

// Every other command that acts on the record refuses the same way, and takes
// nothing away: a removal had withdrawn the plugin's trust and deleted its
// store and bin/ link before refusing to drop its line, an upgrade and a
// removal called a plugin the record lists not managed by rta, a prune
// decided which copy was current without the record's half of the answer,
// and detaching an index took it from under the plugins that came from it.
func TestNothingChangesWhatTheLockRecordsWhenTheLockDoesNotParse(t *testing.T) {
	testData(t)
	bin := hello(t)
	attach(t, helloManifest(t, bin, ""))
	rep, verr := Install(context.Background(), "hello", io.Discard)
	if verr != nil {
		t.Fatal(verr)
	}
	doc := breakLock(t, `{"name":"hello","digest":"`+rep.Digest+`","index":"lab"}`,
		`{"name":"pg","digest":"bbb","index":"official"}`)

	for _, c := range []struct {
		what   string
		change func() *view.Error
	}{
		{"a removal", func() *view.Error { _, verr := Remove("hello"); return verr }},
		{"a removal's dry run", func() *view.Error { _, verr := PreviewRemove("hello"); return verr }},
		{"an upgrade", func() *view.Error {
			_, verr := Upgrade(context.Background(), "hello", io.Discard)
			return verr
		}},
		{"a prune", func() *view.Error { _, verr := Prune(); return verr }},
		{"detaching the index", func() *view.Error { return RemoveIndex("lab") }},
	} {
		refusedOverTheLock(t, c.what, c.change(), doc)
	}
	if got := StoredDigests("hello"); len(got) != 1 || got[0] != rep.Digest {
		t.Errorf("the store holds %v, want %s still", got, rep.Digest)
	}
	if cur, ok := CurrentDigest("hello"); !ok || cur != rep.Digest {
		t.Errorf("bin/ points at %q (%v), want %s still", cur, ok, rep.Digest)
	}
	if !plugintrust.Load().Trusts(rep.Digest) {
		t.Error("the installed plugin's trust was withdrawn")
	}
	if _, attached := IndexByName("lab"); !attached {
		t.Error("the index the plugin came from was detached")
	}
}

// The record is read under its file lock before the first write and held
// until the last, so it cannot stop parsing between the check and the
// rewrite either: the gate runs after everything is fetched and verified and
// before anything lands, which is the widest window there is.
func TestARecordThatStopsParsingMidInstallStillLandsNothing(t *testing.T) {
	testData(t)
	bin := hello(t)
	attach(t, helloManifest(t, bin, ""))
	listed, verr := Resolve("lab/hello")
	if verr != nil {
		t.Fatal(verr)
	}
	var doc string
	breakIt := func(plugin.Plugin) *view.Error {
		doc = breakLock(t, `{"name":"pg","digest":"bbb","index":"official"}`)
		return nil
	}
	_, verr = installFrom(context.Background(), listed, io.Discard, false, breakIt)
	refusedOverTheLock(t, "an install", verr, doc)
	assertNothingInstalled(t, sha256Of(t, bin))
	if _, err := os.Lstat(filepath.Join(BinDir(), pluginhost.BinaryName("hello"))); err == nil {
		t.Error("bin/ links a plugin the refused install was never recorded as installing")
	}
}
