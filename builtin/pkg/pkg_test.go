package pkg

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// fake installs recorded command output and a fixed set of "installed"
// binaries for one test, and restores the real seams afterwards.
type fake struct {
	bins    map[string]bool
	answers map[string]fakeAnswer
	ran     []string
	upgrade [][]string
}

type fakeAnswer struct {
	out  string
	code int
}

func install(t *testing.T, f *fake) {
	t.Helper()
	oldRun, oldLook, oldUp, oldRoot := runCommand, lookPath, runUpgrade, isRoot
	runCommand = func(_ context.Context, name string, args ...string) (string, string, error) {
		key := strings.Join(append([]string{name}, args...), " ")
		f.ran = append(f.ran, key)
		a, ok := f.answers[key]
		if !ok {
			if !f.bins[name] {
				return "", "", &exec.Error{Name: name, Err: exec.ErrNotFound}
			}
			return "", "unscripted: " + key, errors.New("exit status 1")
		}
		if a.code != 0 {
			return a.out, "", fakeExit(a.code)
		}
		return a.out, "", nil
	}
	lookPath = func(name string) (string, error) {
		if f.bins[name] {
			return "/usr/local/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	runUpgrade = func(_ context.Context, argv []string) error {
		f.upgrade = append(f.upgrade, argv)
		return nil
	}
	isRoot = func() bool { return false }
	t.Cleanup(func() { runCommand, lookPath, runUpgrade, isRoot = oldRun, oldLook, oldUp, oldRoot })
}

// fakeExit builds an *exec.ExitError carrying the code, which is what run()
// reads back. The only portable way to mint one is to run a process.
func fakeExit(code int) error {
	cmd := exec.Command("sh", "-c", "exit "+itoa(code))
	return cmd.Run()
}

func req(t *testing.T, capID string, values map[string]any) plugin.Request {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == capID {
			return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), false, false)
		}
	}
	t.Fatalf("no capability %q", capID)
	return plugin.Request{}
}

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

// The line: the reads describe the machine and never take a destination
// from a caller; the upgrade is destructive, scoped, and off the MCP surface.
func TestTheShapeOfTheNamespace(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if !c.HostSpecific || !c.NoPreview {
			t.Errorf("%s must be HostSpecific and NoPreview", c.ID)
		}
		for _, f := range c.Inputs {
			if f.Name == "tools" && !f.Local {
				t.Errorf("%s: the tools list names network sources and must be Local", c.ID)
			}
		}
		switch c.ID {
		case "pkg.upgrade":
			if c.Safety != plugin.Destructive || c.Scope != "target" {
				t.Errorf("pkg.upgrade: Safety=%s Scope=%q", c.Safety, c.Scope)
			}
		default:
			if c.Safety != plugin.Read {
				t.Errorf("%s should be read", c.ID)
			}
		}
	}
}

// None of it is reachable by an agent: every capability, the reads
// included, refuses the MCP surface with a marked refusal — and before it
// runs anything, which is what the empty fake proves.
func TestEveryCapabilityRefusesMCP(t *testing.T) {
	f := &fake{bins: map[string]bool{"brew": true}}
	install(t, f)
	for _, c := range Plugin().Capabilities {
		r := plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: map[string]any{"target": "brew"}}), false, false).
			WithSurface(plugin.SurfaceMCP)
		_, err := c.Run(context.Background(), r)
		ve := view.AsError(err, "x")
		if ve.Code != "pkg.human" || !ve.Refusal {
			t.Errorf("%s over MCP: %+v", c.ID, ve)
		}
	}
	if len(f.ran) != 0 || len(f.upgrade) != 0 {
		t.Errorf("an MCP call ran something: %v %v", f.ran, f.upgrade)
	}
}

func TestOutdatedAcrossManagersWithTheCommandOnEveryRow(t *testing.T) {
	f := &fake{
		bins: map[string]bool{"brew": true, "npm": true, "apt-get": true},
		answers: map[string]fakeAnswer{
			"brew outdated --json=v2": {out: `{"formulae":[{"name":"jq","installed_versions":["1.6"],"current_version":"1.7.1"}],"casks":[]}`},
			"npm outdated -g --json":  {out: `{"typescript":{"current":"5.3.0","wanted":"5.6.0","latest":"5.6.0"}}`, code: 1},
			"apt list --upgradable":   {out: "Listing... Done\ncurl/stable 8.5.0-2 amd64 [upgradable from: 8.4.0-1]\n"},
		},
	}
	install(t, f)
	v, err := outdatedCapability().Run(context.Background(), req(t, "pkg.outdated", nil))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	want := map[string][]string{
		"jq":         {"brew", "1.6", "1.7.1", "outdated", "brew upgrade jq"},
		"curl":       {"apt", "8.4.0-1", "8.5.0-2", "outdated", "sudo apt-get install --only-upgrade -y curl"},
		"typescript": {"npm", "5.3.0", "5.6.0", "outdated", "npm install -g typescript@latest"},
	}
	if len(tbl.Rows) != len(want) {
		t.Fatalf("rows = %v", tbl.Rows)
	}
	for _, r := range tbl.Rows {
		w := want[r[1]]
		if r[0] != w[0] || r[2] != w[1] || r[3] != w[2] || r[4] != w[3] || r[5] != w[4] {
			t.Errorf("row %v, want %v", r, w)
		}
	}
	// Inventory order: apt before brew before npm.
	if tbl.Rows[0][0] != "brew" || tbl.Rows[1][0] != "apt" || tbl.Rows[2][0] != "npm" {
		t.Errorf("order = %v %v %v", tbl.Rows[0][0], tbl.Rows[1][0], tbl.Rows[2][0])
	}
}

func TestAFailedManagerIsARowNotASilence(t *testing.T) {
	f := &fake{bins: map[string]bool{"brew": true}, answers: map[string]fakeAnswer{
		"brew outdated --json=v2": {out: "not json"},
	}}
	install(t, f)
	v, err := outdatedCapability().Run(context.Background(), req(t, "pkg.outdated", nil))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	if len(tbl.Rows) != 1 || tbl.Rows[0][0] != "brew" || !strings.HasPrefix(tbl.Rows[0][4], "fail ") {
		t.Errorf("rows = %v", tbl.Rows)
	}
}

func TestRegistryBackedManagersCompareAgainstTheirRegistry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pypi/black/json":
			w.Write([]byte(`{"info":{"version":"24.8.0"}}`))
		case "/pypi/ruff/json":
			w.Write([]byte(`{"info":{"version":"0.6.0"}}`))
		case "/api/v1/crates/ripgrep":
			w.Write([]byte(`{"crate":{"max_stable_version":"14.1.1"}}`))
		case "/github.com/junegunn/fzf/@latest":
			w.Write([]byte(`{"Version":"v0.55.0"}`))
		case "/prettier/latest":
			w.Write([]byte(`{"version":"3.3.3"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newRegistryClient()
	c.pypi, c.crates, c.gomod, c.npm = srv.URL, srv.URL, srv.URL, srv.URL

	f := &fake{
		bins: map[string]bool{"pipx": true, "cargo": true, "go": true, "bun": true},
		answers: map[string]fakeAnswer{
			"pipx list --json":     {out: `{"venvs":{"black":{"metadata":{"main_package":{"package":"black","package_version":"24.1.0"}}},"ruff":{"metadata":{"main_package":{"package":"ruff","package_version":"0.6.0"}}}}}`},
			"cargo install --list": {out: "ripgrep v14.0.0:\n    rg\n"},
			"go env GOBIN":         {out: "\n"},
			"go env GOPATH":        {out: t.TempDir() + "\n"},
			"bun pm ls -g":         {out: "/home/x/.bun/install/global node_modules\n├── prettier@3.0.0\n"},
		},
	}
	install(t, f)
	gopath := strings.TrimSpace(f.answers["go env GOPATH"].out)
	os.MkdirAll(filepath.Join(gopath, "bin"), 0o755)
	os.WriteFile(filepath.Join(gopath, "bin", "fzf"), []byte("x"), 0o755)
	f.answers["go version -m "+filepath.Join(gopath, "bin", "fzf")] = fakeAnswer{out: "fzf: go1.22\n\tpath\tgithub.com/junegunn/fzf\n\tmod\tgithub.com/junegunn/fzf\tv0.50.0\th1:abc\n"}

	l := collect(context.Background(), c, "")
	got := map[string]string{}
	for _, r := range l.rows {
		got[r.Manager+"/"+r.Name] = r.Current + "→" + r.Latest
	}
	want := map[string]string{"pipx/black": "24.1.0→24.8.0", "cargo/ripgrep": "14.0.0→14.1.1", "go/fzf": "v0.50.0→v0.55.0", "bun/prettier": "3.0.0→3.3.3"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	if _, has := got["pipx/ruff"]; has {
		t.Error("ruff is current and must not be listed")
	}
	if len(l.failed) != 0 {
		t.Errorf("failed: %v", l.failed)
	}
}

func TestUpgradeRunsOneManagerAndNeverSudo(t *testing.T) {
	f := &fake{bins: map[string]bool{"brew": true, "apt-get": true, "cargo": true}}
	install(t, f)

	v, err := runUpgradeCapability(context.Background(), req(t, "pkg.upgrade", map[string]any{"target": "brew", "package": "jq"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.upgrade) != 1 || strings.Join(f.upgrade[0], " ") != "brew upgrade jq" || !strings.Contains(v.(view.Text).Body, "brew upgrade jq") {
		t.Errorf("ran %v, said %v", f.upgrade, v)
	}

	_, err = runUpgradeCapability(context.Background(), req(t, "pkg.upgrade", map[string]any{"target": "apt"}))
	ve := view.AsError(err, "x")
	if ve.Code != "pkg.upgrade.root" || !strings.Contains(ve.Hint, "sudo apt-get upgrade -y") {
		t.Errorf("apt as non-root = %+v", ve)
	}
	if len(f.upgrade) != 1 {
		t.Error("a root-needing manager ran without root")
	}

	_, err = runUpgradeCapability(context.Background(), req(t, "pkg.upgrade", map[string]any{"target": "cargo"}))
	if ve := view.AsError(err, "x"); ve.Code != "pkg.upgrade.package" {
		t.Errorf("cargo without a package = %+v", ve)
	}
	_, err = runUpgradeCapability(context.Background(), req(t, "pkg.upgrade", map[string]any{"target": "nope"}))
	if ve := view.AsError(err, "x"); ve.Code != "pkg.upgrade.unknown" {
		t.Errorf("unknown target = %+v", ve)
	}
}

func TestUpgradeDryRunRunsNothing(t *testing.T) {
	f := &fake{bins: map[string]bool{"brew": true}}
	install(t, f)
	r := plugin.NewRequest(plugin.Resolve(upgradeCapability(), plugin.Inputs{Caller: map[string]any{"target": "brew"}}), true, false)
	v, err := runUpgradeCapability(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v.(view.Text).Body, "would run: brew upgrade") || len(f.upgrade) != 0 {
		t.Errorf("dry run: %v ran %v", v, f.upgrade)
	}
}

func TestToolsGrammarAndListing(t *testing.T) {
	if _, verr := parseTools([]string{"kubectl-neat=github:itaysk/kubectl-neat", "bad"}); verr == nil || verr.Code != "pkg.tools.entry" {
		t.Errorf("bad entry = %+v", verr)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/junegunn/fzf/releases/latest" {
			w.Write([]byte(`{"tag_name":"v0.55.0","assets":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := newRegistryClient()
	c.github = srv.URL
	f := &fake{bins: map[string]bool{"fzf": true}, answers: map[string]fakeAnswer{
		"fzf --version": {out: "0.50.0 (brew)\n"},
	}}
	install(t, f)
	states, verr := readTools(context.Background(), c, []string{"fzf=github:junegunn/fzf", "ghost=github:no/such"})
	if verr != nil {
		t.Fatal(verr)
	}
	if states[0].Installed != "0.50.0" || states[0].Latest != "0.55.0" || !states[0].behind() {
		t.Errorf("fzf = %+v", states[0])
	}
	if states[1].Note != "not on $PATH" && states[1].Note != "no release on GitHub" {
		t.Errorf("ghost = %+v", states[1])
	}
	tbl := toolsTable(states)
	if tbl.Rows[0][4] != "outdated" || tbl.Columns[0].Name != "target" {
		t.Errorf("table = %v", tbl)
	}
}

// The install path, end to end against a fake GitHub and a real archive:
// the digest the release publishes is checked, the member is found under a
// directory, and the binary lands atomically with the executable bit.
func TestInstallToolVerifiesAndPlaces(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	archive, digest := tarGz(t, "fzf-0.55.0/fzf", "#!/bin/sh\necho new\n")
	assetName := "fzf-0.55.0-" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/junegunn/fzf/releases/latest":
			w.Write([]byte(`{"tag_name":"v0.55.0","assets":[{"name":"` + assetName + `","browser_download_url":"` + srv.URL + `/dl/` + assetName + `","size":123},{"name":"checksums.txt","browser_download_url":"` + srv.URL + `/dl/checksums.txt"}]}`))
		case "/dl/" + assetName:
			w.Write(archive)
		case "/dl/checksums.txt":
			w.Write([]byte(digest + "  " + assetName + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newRegistryClient()
	c.github = srv.URL

	dest := filepath.Join(t.TempDir(), "bin", "fzf")
	os.MkdirAll(filepath.Dir(dest), 0o755)
	os.WriteFile(dest, []byte("old"), 0o755)
	f := &fake{bins: map[string]bool{}}
	install(t, f)
	lookPath = func(name string) (string, error) {
		if name == "fzf" {
			return dest, nil
		}
		return "", exec.ErrNotFound
	}
	// The download goes through plugindist.Fetch, which speaks https only;
	// a test server is http, so the URL check is what this asserts first.
	_, verr := installTool(context.Background(), c, tool{Bin: "fzf", Owner: "junegunn", Repo: "fzf"}, false, false)
	if verr == nil || verr.Code != "pkg.tool.url" {
		t.Fatalf("an http asset must be refused: %+v", verr)
	}
}

func tarGz(t *testing.T, member, content string) ([]byte, string) {
	t.Helper()
	dir := t.TempDir()
	full := filepath.Join(dir, member)
	os.MkdirAll(filepath.Dir(full), 0o755)
	os.WriteFile(full, []byte(content), 0o755)
	out := filepath.Join(dir, "a.tar.gz")
	if err := exec.Command("tar", "-czf", out, "-C", dir, filepath.Dir(member)).Run(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(out)
	return raw, "0000000000000000000000000000000000000000000000000000000000000000"
}

func TestMemberNamedFindsTheBinaryUnderADirectory(t *testing.T) {
	archive, _ := tarGz(t, "fzf-0.55.0/fzf", "x")
	m, verr := memberNamed(strings.NewReader(string(archive)), "fzf")
	if verr != nil || m != "fzf-0.55.0/fzf" {
		t.Errorf("member = %q, %v", m, verr)
	}
	if _, verr := memberNamed(strings.NewReader(string(archive)), "other"); verr == nil || verr.Code != "pkg.tool.archive" {
		t.Errorf("missing member = %+v", verr)
	}
}

func TestOSReadingOnLinuxShapes(t *testing.T) {
	f := &fake{bins: map[string]bool{"dpkg-query": true, "uname": true}, answers: map[string]fakeAnswer{
		"uname -r": {out: "6.8.0-40-generic\n"},
		"dpkg-query -W -f ${Package}\n linux-image-*": {out: "linux-image-6.8.0-40-generic\nlinux-image-6.8.0-45-generic\nlinux-image-generic\n"},
	}}
	install(t, f)
	st := readLinux(context.Background())
	if st.KernelRunning != "6.8.0-40-generic" || st.KernelNewest != "6.8.0-45-generic" || !st.kernelBehind() || !st.RebootRequired {
		t.Errorf("linux state = %+v", st)
	}
}

func TestMacOSUpdatesParse(t *testing.T) {
	f := &fake{bins: map[string]bool{"softwareupdate": true}, answers: map[string]fakeAnswer{
		"softwareupdate --list": {out: "Software Update Tool\n\nFinding available software\nSoftware Update found the following new or updated software:\n* Label: macOS Sonoma 14.6.1-23G93\n\tTitle: macOS Sonoma 14.6.1, Version: 14.6.1, Size: 1234KiB, Recommended: YES, Action: restart,\n"},
	}}
	install(t, f)
	st := readMacOS(context.Background())
	if len(st.Updates) != 1 || st.Updates[0].Version != "14.6.1" || !st.Updates[0].Restart || !st.RebootRequired {
		t.Errorf("macos state = %+v", st)
	}
}

func TestSemverLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		less bool
	}{{"1.6", "1.7.1", true}, {"v0.50.0", "v0.55.0", true}, {"24.8.0", "24.8.0", false}, {"6.8.0-45", "6.8.0-40", false}, {"1.10", "1.9", false}} {
		if got := semverLess(c.a, c.b); got != c.less {
			t.Errorf("semverLess(%q,%q) = %v", c.a, c.b, got)
		}
	}
}

func TestManagersListsPresentAndAbsentWithVersions(t *testing.T) {
	f := &fake{
		bins: map[string]bool{"brew": true, "go": true, "apt-get": true, "pacman": true},
		answers: map[string]fakeAnswer{
			"brew --version":    {out: "Homebrew 4.3.10\n"},
			"go version":        {out: "go version go1.23.0 darwin/arm64\n"},
			"apt-get --version": {out: "apt 2.7.14 (amd64)\nSupported modules:\n"},
			"pacman --version":  {out: "\n .--.                  Pacman v6.1.0 - libalpm v14.0.0\n"},
		},
	}
	install(t, f)
	v, err := managersCapability().Run(context.Background(), req(t, "pkg.managers", nil))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	if len(tbl.Rows) != len(managers()) {
		t.Fatalf("rows = %d, want one per known manager", len(tbl.Rows))
	}
	got := map[string][]string{}
	for _, r := range tbl.Rows {
		got[r[0]] = r
	}
	for name, want := range map[string][]string{
		"brew":   {"brew", "/usr/local/bin/brew", "4.3.10", "-", "ok"},
		"go":     {"go", "/usr/local/bin/go", "1.23.0", "-", "ok"},
		"apt":    {"apt-get", "/usr/local/bin/apt-get", "2.7.14", "yes", "ok"},
		"pacman": {"pacman", "/usr/local/bin/pacman", "6.1.0", "yes", "ok"},
		"npm":    {"npm", "-", "-", "-", "absent"},
		"dnf":    {"dnf", "-", "-", "yes", "absent"},
	} {
		r := got[name]
		if r == nil || r[1] != want[0] || r[2] != want[1] || r[3] != want[2] || r[4] != want[3] || r[5] != want[4] {
			t.Errorf("%s: row %v, want %v", name, r, want)
		}
	}
	for _, ran := range f.ran {
		if strings.HasPrefix(ran, "npm ") || strings.HasPrefix(ran, "dnf ") {
			t.Errorf("an absent manager was asked: %s", ran)
		}
	}
}

func TestParseVersionReadsTheFirstThingThatLooksLikeOne(t *testing.T) {
	for in, want := range map[string]string{
		"10.8.2\n":                                 "10.8.2",
		"uv 0.4.0 (Homebrew 2024-08-20)\n":         "0.4.0",
		"apk-tools 2.14.0, compiled for x86_64.\n": "2.14.0",
		"mise 2024.9.0 macos-arm64\n":              "2024.9.0",
		"cargo 1.80.0 (376290515 2024-07-16)\n":    "1.80.0",
		"nothing here\n":                           "-",
		"":                                         "-",
	} {
		if got := parseVersion(in); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
