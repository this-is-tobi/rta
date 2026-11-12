package pkg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// linked lays a binary out the way a package store does: the file at
// root/target, and at root/link a link to it, spelled relative to the link's
// directory when relative is set, as Homebrew spells its links. It returns the
// link's path, which is what a lookup on $PATH finds.
func linked(t *testing.T, root, link, target string, relative bool) string {
	t.Helper()
	file, at := filepath.Join(root, target), filepath.Join(root, link)
	for _, dir := range []string{filepath.Dir(file), filepath.Dir(at)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	to := file
	if relative {
		rel, err := filepath.Rel(filepath.Dir(at), file)
		if err != nil {
			t.Fatal(err)
		}
		to = rel
	}
	if err := os.Symlink(to, at); err != nil {
		t.Fatal(err)
	}
	return at
}

// **A listed tool that does not know --version is read from where it is
// kept.** krew's plugins are cobra commands, and most have only a `version`
// subcommand, which rta never runs: for cmctl and kubectl it asks the
// cluster in the current context. Of seven plugins krew had installed, stern
// was the one whose version showed, and the six others read unknown.
//
// A failing --version is not an answer either: cmctl printed its usage to
// stdout, and the number in one of its examples read as the installed
// version, ahead of the store that names the real one.
func TestAToolThatDoesNotKnowTheFlagIsReadFromItsStore(t *testing.T) {
	root := t.TempDir()
	at := map[string]string{
		"kubectl-cnpg":         linked(t, root, "krew/bin/kubectl-cnpg", "krew/store/cnpg/v1.30.1/kubectl-cnpg", false),
		"kubectl-cert_manager": linked(t, root, "krew/bin/kubectl-cert_manager", "krew/store/cert-manager/v2.1.0/cmctl", false),
		"kubectl-stern":        linked(t, root, "krew/bin/kubectl-stern", "krew/store/stern/v1.33.0/stern", false),
	}
	c := newRegistryClient()
	c.tags = newTagCache(time.Now)
	c.web = latestTags(t, map[string]string{
		"cloudnative-pg/cloudnative-pg": "v1.30.1", "cert-manager/cmctl": "v2.4.0", "stern/stern": "v1.34.0",
	})
	f := &fake{bins: map[string]bool{}, answers: map[string]fakeAnswer{
		"kubectl-cnpg --version":         {code: 1, stderr: "Error: unknown flag: --version\n"},
		"kubectl-cert_manager --version": {code: 1, out: "error: unknown flag: --version\nUsage:\n  cmctl check api --wait 1.5m\n"},
		// The binary's own answer wins over the store's: it says what runs.
		"kubectl-stern --version": {out: "version: 1.34.0\ncommit: b6f1226\n"},
	}}
	for bin := range at {
		f.bins[bin] = true
	}
	install(t, f)
	versionOnDisk = storeVersion
	lookPath = func(name string) (string, error) {
		if p, ok := at[name]; ok {
			return p, nil
		}
		return "", exec.ErrNotFound
	}

	states, verr := readTools(context.Background(), c, []string{
		"kubectl-cnpg=github:cloudnative-pg/cloudnative-pg",
		"kubectl-cert_manager=github:cert-manager/cmctl",
		"kubectl-stern=github:stern/stern",
	})
	if verr != nil {
		t.Fatal(verr)
	}
	rows := toolsTable(states).Rows
	for i, want := range []struct{ installed, status string }{
		{"1.30.1", "ok"}, {"2.1.0", "outdated"}, {"1.34.0", "ok"},
	} {
		if rows[i][2] != want.installed || rows[i][4] != want.status {
			t.Errorf("%s: Installed %q Status %q, want %q %q", rows[i][0], rows[i][2], rows[i][4], want.installed, want.status)
		}
	}
	for _, ran := range f.ran {
		if !strings.HasSuffix(ran, " --version") {
			t.Errorf("a tool was run as %q; --version is the one form rta asks", ran)
		}
	}
}

// **A version in a path is the binary's only where its link leads, and only
// under a directory that names it.** A runtime's version holds everything
// installed under it, and a version manager's shim leads to the manager.
func TestAStoreVersionIsTakenOnlyFromWhereTheLinkLeads(t *testing.T) {
	for _, tc := range []struct {
		name, link, target string
		relative           bool
		want               string
	}{
		{"krew", "krew/bin/kubectl-cnpg", "krew/store/cnpg/v1.30.1/kubectl-cnpg", false, "1.30.1"},
		{"a plugin named for its host", "krew/bin/kubectl-df_pv", "krew/store/df-pv/v0.5.0/df-pv", false, "0.5.0"},
		{"homebrew", "homebrew/bin/stern", "homebrew/Cellar/stern/1.34.0/bin/stern", true, "1.34.0"},
		// npm's global under mise's node: the link sits in node's version.
		{"a package under a runtime", "mise/installs/node/22.1.0/bin/ts-node",
			"mise/installs/node/22.1.0/lib/node_modules/ts-node/dist/bin.js", true, ""},
		// mise's shim is the mise binary, kept in its own version.
		{"a shim", "mise/shims/kubectl", "homebrew/Cellar/mise/2026.1.0/bin/mise", false, ""},
		{"a version that names another binary", "opt/bin/helm", "opt/tools/kubectl/1.31.0/helm", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			link := linked(t, root, tc.link, tc.target, tc.relative)
			if got := storeVersion(link, filepath.Base(tc.link)); got != tc.want {
				t.Errorf("storeVersion(%s -> %s) = %q, want %q", tc.link, tc.target, got, tc.want)
			}
		})
	}

	// A binary that is a file, not a link, is read from nothing in its path:
	// the version directory holds the directory on $PATH itself.
	root := t.TempDir()
	file := filepath.Join(root, "tool", "1.2.3", "bin", "tool")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := storeVersion(file, "tool"); got != "" {
		t.Errorf("a binary that is no link read %q from its path", got)
	}
}

// **A first install says when the shell will not find what it placed.** A
// tool not yet on $PATH goes to ~/.local/bin, which macOS does not put on
// $PATH: the install said it was done, the shell found no such command, and
// the next pkg.tools said "not on $PATH" with nothing between to say why.
func TestAFirstInstallSaysWhenItsDirectoryIsNotOnPath(t *testing.T) {
	goos, arch := here(t)
	c := servedRelease(t, "o", "tool", "tool-"+goos+"-"+arch, []byte("#!/bin/sh\necho new\n"))
	install(t, &fake{bins: map[string]bool{}})
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".local", "bin")
	dryRun := func() string {
		t.Helper()
		v, verr := installTool(context.Background(), plugin.SurfaceCLI, c, tool{Bin: "tool", Owner: "o", Repo: "tool"}, false, true)
		if verr != nil {
			t.Fatal(verr)
		}
		return v.(view.Text).Body
	}

	t.Setenv("PATH", "/usr/bin:/bin")
	if body := dryRun(); !strings.Contains(body, bin+" is not on $PATH") {
		t.Errorf("a first install into a directory off $PATH said:\n%s", body)
	}
	t.Setenv("PATH", "/usr/bin:"+bin+"/")
	if body := dryRun(); strings.Contains(body, "not on $PATH") {
		t.Errorf("a first install into a directory on $PATH said:\n%s", body)
	}
}

// **An upgrade never puts a file where a link was.** The upgrade renames its
// file over the destination, and over krew's link that left krew's store
// holding the old version and its receipt naming a version that was no longer
// the one on $PATH. The link is refused before GitHub is asked anything, dry
// run or not, and is left as it was.
func TestAnUpgradeNeverReplacesALink(t *testing.T) {
	root := t.TempDir()
	// Relative, as Homebrew writes its links: the refusal names the path it
	// resolves to, not ../store/….
	link := linked(t, root, "krew/bin/kubectl-cnpg", "krew/store/cnpg/v1.30.1/kubectl-cnpg", true)
	install(t, &fake{bins: map[string]bool{}})
	lookPath = func(name string) (string, error) {
		if name == "kubectl-cnpg" {
			return link, nil
		}
		return "", exec.ErrNotFound
	}
	logged := newRequestLog(t, nil)
	c := newRegistryClient()
	c.tags = newTagCache(time.Now)
	c.github, c.web = logged.URL, logged.URL
	for _, dryRun := range []bool{true, false} {
		_, verr := installTool(context.Background(), plugin.SurfaceCLI, c,
			tool{Bin: "kubectl-cnpg", Owner: "cloudnative-pg", Repo: "cloudnative-pg"}, false, dryRun)
		if verr == nil || verr.Code != "pkg.tool.link" {
			t.Fatalf("dry run %v: an upgrade over a link = %v, want pkg.tool.link", dryRun, verr)
		}
		if !strings.Contains(verr.Hint, "kubectl krew upgrade") {
			t.Errorf("hint = %q, want it to name what upgrades a krew plugin", verr.Hint)
		}
		if !strings.Contains(verr.Message, "/krew/store/cnpg/v1.30.1/kubectl-cnpg") || strings.Contains(verr.Message, "..") {
			t.Errorf("message = %q, want the absolute path the link leads to", verr.Message)
		}
	}
	if n := logged.total(); n != 0 {
		t.Errorf("GitHub was asked %d times for an upgrade that was refused", n)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link is no longer a link: %v, %v", info, err)
	}
}
