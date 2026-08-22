package pathguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rooted(t *testing.T) (*Guard, string) {
	t.Helper()
	root := t.TempDir()
	// t.TempDir is under /var on macOS, which is itself a symlink to
	// /private/var — so a guard that did not resolve its roots would refuse
	// every path under its own root. That is not a hypothetical: it is the
	// first thing that happened.
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	return g, root
}

func TestInsideTheRootIsAllowed(t *testing.T) {
	g, root := rooted(t)
	for _, p := range []string{
		root,
		filepath.Join(root, "file.txt"),
		filepath.Join(root, "a", "b", "c", "deep.txt"),
		filepath.Join(root, "does", "not", "exist", "yet"),
	} {
		if _, err := g.Check("path", p); err != nil {
			t.Errorf("%q was refused: %v", p, err)
		}
	}
}

func TestOutsideTheRootIsRefused(t *testing.T) {
	g, root := rooted(t)
	for _, p := range []string{
		"/etc/passwd",
		"/etc",
		filepath.Join(root, "..", "elsewhere"),
		filepath.Join(root, "..", "..", "etc", "passwd"),
	} {
		_, err := g.Check("path", p)
		if err == nil {
			t.Errorf("%q was allowed", p)
			continue
		}
		if err.Code != "core.mcp.path.outside" {
			t.Errorf("%q: code = %q", p, err.Code)
		}
	}
}

// The escape a lexical check misses, and the reason resolve walks to the
// deepest existing ancestor rather than calling filepath.Clean and comparing
// strings. A link inside the root passes any prefix test and then opens
// whatever it points at.
func TestASymlinkOutOfTheRootIsRefused(t *testing.T) {
	g, root := rooted(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A link to the file itself.
	link := filepath.Join(root, "innocent.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := g.Check("path", link); err == nil {
		t.Error("a symlink to a file outside the root was allowed")
	}

	// And a link to the directory, with the interesting part in the tail —
	// the case where the path being checked does not itself exist as a link.
	dirLink := filepath.Join(root, "elsewhere")
	if err := os.Symlink(outside, dirLink); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Check("path", filepath.Join(dirLink, "secret.txt")); err == nil {
		t.Error("a path through a symlinked directory escaped the root")
	}
	if _, err := g.Check("path", filepath.Join(dirLink, "not-created-yet.txt")); err == nil {
		t.Error("a non-existent path through a symlinked directory escaped the root")
	}
}

// "/home/user" is a string prefix of "/home/username", so a prefix test hands
// one account's files to another's root.
func TestASiblingWithASharedPrefixIsRefused(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	sibling := filepath.Join(base, "project-secrets")
	for _, d := range []string{root, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Check("path", filepath.Join(sibling, "creds")); err == nil {
		t.Errorf("%q was allowed under root %q", sibling, root)
	}
}

// A relative value resolves under the working directory by construction, so
// anything that is not an absolute path passes untouched. That is what lets
// one field carry "example.com:443" or a PEM path and be confined either way.
//
// What this deliberately does NOT do is decide whether a string is a path at
// all. Check refuses any absolute location outside the roots, full stop, and
// it cannot tell "/etc/passwd" from a base64 payload that happens to start
// with "/" — base64's alphabet contains it, and a JPEG encodes to a leading
// "/9j/". Which arguments are paths is the caller's question, answered by
// Field.Type == Path in the bridge, which is why that type being closed and
// mandatory (ADR 0011) is load-bearing here and not only documentation.
// TestANonPathArgumentIsNotConfined in internal/mcp pins the other half.
func TestRelativeValuesAreNotRefused(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(wd)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{
		"example.com:443",
		"https://example.com/a/b?c=d",
		"kv.get",
		"sub/dir/file.txt",
		"",
		"   ",
	} {
		if _, err := g.Check("value", v); err != nil {
			t.Errorf("%q was refused: %v", v, err)
		}
	}
}

// The one denylist entry, and it holds even inside a root: an operator who
// starts the server from their home directory has put the age identity back
// in scope without meaning to.
func TestRtasOwnDataDirectoryIsAlwaysRefused(t *testing.T) {
	data := t.TempDir()
	t.Setenv("RTA_DATA_DIR", data)
	// Rooted at the parent, so the data directory is inside the root and only
	// the explicit denial can refuse it.
	g, err := New(filepath.Dir(data))
	if err != nil {
		t.Fatal(err)
	}
	_, err2 := g.Check("path", filepath.Join(data, "kv.identity"))
	if err2 == nil {
		t.Fatal("a path inside rta's data directory was allowed")
	}
	if err2.Code != "core.mcp.path.protected" {
		t.Errorf("code = %q, want the protected-path code", err2.Code)
	}
	if !strings.Contains(err2.Hint, "secret store") {
		t.Errorf("the hint should say why: %q", err2.Hint)
	}
}

// The zero value allows everything, which is what the CLI and the TUI use:
// there is a person there who can already read their own files, and a guard
// would be an obstacle with no threat behind it.
func TestTheZeroGuardAllowsEverything(t *testing.T) {
	var g *Guard
	if _, err := g.Check("path", "/etc/passwd"); err != nil {
		t.Errorf("a nil guard refused something: %v", err)
	}
	if _, err := (&Guard{}).Check("path", "/etc/passwd"); err != nil {
		t.Errorf("a zero guard refused something: %v", err)
	}
}

func TestTildeExpands(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := ExpandTilde("~/x"); got != filepath.Join(home, "x") {
		t.Errorf("ExpandTilde(~/x) = %q", got)
	}
	if got := ExpandTilde("~"); got != home {
		t.Errorf("ExpandTilde(~) = %q", got)
	}
	// Not another account's home, and not a local file that starts with "~".
	if got := ExpandTilde("~other/x"); got != "~other/x" {
		t.Errorf("ExpandTilde(~other/x) = %q, want it left alone", got)
	}

	g, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Check("path", "~/.ssh/id_rsa"); err == nil {
		t.Error("~/.ssh/id_rsa was allowed under a temp-dir root; the tilde was not expanded")
	}
}

func TestAGuardNeedsARoot(t *testing.T) {
	if _, err := New(); err == nil {
		t.Error("a guard with no roots was built; it would allow everything while looking enforced")
	}
}

// A case variant of the data directory must be refused.
//
// The deny set compared paths as strings, and macOS is case-insensitive by
// default. Reproduced before the fix, on this machine: with the data
// directory denied, `…/rta/grants.key` was refused and `…/RTA/grants.key` was
// allowed — and read the same bytes. That is the seal key for every grant,
// named by an agent that changed one letter's case.
//
// The test asserts the outcome on whatever filesystem it runs on rather than
// assuming one: if the variant does not resolve to the same file, the
// filesystem is case-sensitive and there was nothing to defend.
func TestACaseVariantOfTheDataDirectoryIsRefused(t *testing.T) {
	base := t.TempDir()
	data := filepath.Join(base, "rta")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_DATA_DIR", data)
	secret := filepath.Join(data, "grants.key")
	if err := os.WriteFile(secret, []byte("KEY"), 0o600); err != nil {
		t.Fatal(err)
	}

	g, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, verr := g.Check("out", secret); verr == nil {
		t.Fatal("the exact path was allowed, so the deny set is not working at all")
	}

	variant := filepath.Join(base, "RTA", "grants.key")
	reachesTheSameFile := false
	if b, rerr := os.ReadFile(variant); rerr == nil && string(b) == "KEY" {
		reachesTheSameFile = true
	}
	_, verr := g.Check("out", variant)
	switch {
	case reachesTheSameFile && verr == nil:
		t.Errorf("%q reads the seal key and was allowed", variant)
	case reachesTheSameFile && verr.Code != "core.mcp.path.protected":
		t.Errorf("refused for the wrong reason: %s", verr.Code)
	case !reachesTheSameFile:
		t.Logf("case-sensitive filesystem: %q names nothing, so there is nothing to defend", variant)
	}

	// A directory that merely starts with the same letters is not inside it.
	// The identity walk must not over-refuse.
	sibling := filepath.Join(base, "rta-notes")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, verr := g.Check("out", filepath.Join(sibling, "x.md")); verr != nil {
		t.Errorf("a sibling directory was refused: %v", verr)
	}
}

// `..` after a directory symlink used to escape every root, and the denylist
// with it.
//
// resolve called filepath.Abs and filepath.Clean before consulting the
// filesystem, and Clean cancels ".." against the component before it —
// lexically. So a directory symlink was gone from the string by the time
// EvalSymlinks ran, and the guard judged a path the kernel would never
// traverse. `root/link/id_rsa` was correctly refused; `root/link/../secrets/
// id_rsa` was allowed, and read the same bytes, because the kernel resolves
// link first and *then* applies "..".
//
// The paths here are built by concatenation, not filepath.Join. That is not
// style: Join calls Clean, so the crafted spelling cannot be expressed with
// it — which is exactly why the existing symlink tests above, which all use
// Join, could not catch this and still passed.
func TestDotDotAfterASymlinkDoesNotEscapeTheRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	secrets := filepath.Join(base, "secrets")
	for _, d := range []string{root, secrets} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(secrets, "id_rsa"), []byte("PRIVATE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secrets, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, verr := g.Check("path", root+"/link/id_rsa"); verr == nil {
		t.Fatal("the direct spelling was allowed, so this test proves nothing")
	}
	if _, verr := g.Check("path", root+"/link/../secrets/id_rsa"); verr == nil {
		t.Error("`..` after a symlink escaped the root")
	}
}

// The same spelling reached rta's own data directory, where the grant seal
// key lives — the one thing the denylist exists to protect.
func TestDotDotAfterASymlinkDoesNotReachTheDataDirectory(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	data := filepath.Join(base, "state", "rta")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "grants.key"), []byte("SEAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_DATA_DIR", data)
	if err := os.Symlink(filepath.Join(base, "state"), filepath.Join(root, "s")); err != nil {
		t.Fatal(err)
	}
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, verr := g.Check("path", root+"/s/rta/grants.key"); verr == nil {
		t.Fatal("the direct spelling was allowed, so this test proves nothing")
	}
	if _, verr := g.Check("path", root+"/s/../state/rta/grants.key"); verr == nil {
		t.Error("`..` after a symlink reached the seal key")
	}
}

// Each real directory level under a root bought one more "..", so the reach
// was arbitrary rather than limited to the link's parent.
func TestDotDotDoesNotWalkUpOnceLevelPerRealDirectory(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(deep, "link")); err != nil {
		t.Fatal(err)
	}
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, verr := g.Check("path", root+"/a/b/link/../../etc/hosts"); verr == nil {
		t.Error("a crafted `..` chain reached outside the root")
	}
}

// Check hands back the path it judged, so a caller can open that rather than
// the spelling it was given. Two readers of one string is how the previous
// bug became exploitable at all: builtin/fs survived it only because its own
// resolvePath happened to Clean the same way.
func TestCheckReturnsTheResolvedPathItJudged(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	inner := filepath.Join(root, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inner, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	got, verr := g.Check("path", root+"/alias/notes.md")
	if verr != nil {
		t.Fatalf("a path inside the root was refused: %v", verr)
	}
	want, err := filepath.EvalSymlinks(inner)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(want, "notes.md") {
		t.Errorf("Check returned %q, want the resolved %q", got, filepath.Join(want, "notes.md"))
	}
}

// A path that does not exist yet still resolves — the property the original
// walk-up-to-the-deepest-ancestor code was written for, and the one a
// component-wise rewrite could easily lose. --out names a file to create.
func TestAPathThatDoesNotExistYetStillResolves(t *testing.T) {
	root := t.TempDir()
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, verr := g.Check("out", filepath.Join(root, "nope", "deeper", "report.md")); verr != nil {
		t.Errorf("a path that does not exist yet was refused: %v", verr)
	}
}
