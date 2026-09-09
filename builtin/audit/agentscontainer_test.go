package audit

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// recipeArgs is the container recipe from docs/30-boundary/20-mcp.md, verbatim.
// Tests start from it and remove one thing at a time, so each case says exactly
// which part of the documented shape it is about.
func recipeArgs() []string {
	return []string{
		"run", "--rm", "-i",
		"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--network", "none",
		"-v", "rta-home:/rta-home",
		"-v", "${workspaceFolder}:/work:ro",
		"-e", "RTA_CONFIG=/rta-home/config.yaml",
		"-e", "RTA_DATA_DIR=/rta-home",
		"-w", "/work",
		"ghcr.io/this-is-tobi/rta:latest", "mcp", "serve", "--as", "sandboxed", "--root", "/work",
	}
}

// without returns the recipe minus one flag and any value that belongs to it.
func without(args []string, flag string, valued bool) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			if valued {
				i++
			}
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func containerConfig(command string, args []string) string {
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"rta": map[string]any{"command": command, "args": args},
	}})
	return string(b)
}

// containerFindings runs the audit over one container declaration and returns
// every row about it — not a map keyed by name, because a single server
// legitimately produces several findings and the point of most of these tests
// is which ones.
func containerFindings(t *testing.T, command string, args []string) [][]string {
	t.Helper()
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {containerConfig(command, args), 0o600}})

	v, err := runClients(t.Context(), req(map[string]any{}).WithSurface(plugin.SurfaceCLI), testCatalog)
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("want a Table, got %s", view.TypeOf(v))
	}
	var out [][]string
	for _, row := range tbl.Rows {
		if row[0] == "rta" {
			out = append(out, row)
		}
	}
	return out
}

func worstOf(rows [][]string) string {
	worst := "ok"
	for _, r := range rows {
		switch r[1] {
		case "fail":
			return "fail"
		case "warn":
			worst = "warn"
		}
	}
	return worst
}

func joined(rows [][]string) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Join(r, " ") + "\n")
	}
	return b.String()
}

// The recipe as documented is the thing this grades against, so it has to come
// out clean. A check that flags its own documentation is a check nobody keeps.
func TestTheDocumentedRecipeIsNotAFinding(t *testing.T) {
	rows := containerFindings(t, "docker", recipeArgs())
	if got := worstOf(rows); got != "ok" {
		t.Errorf("the documented recipe graded %s:\n%s", got, joined(rows))
	}
}

// **Not optional**, in the docs' own bold. Without them the config path falls
// back to ./.rta.yaml, which is not honoured, so profiles: and plugins: are
// ignored and every plugin runs with its declared defaults — the worst failure
// available here, because it looks like it worked.
// Only for the published narrow image, whose ENV this repository defines: it
// sets PATH and nothing else, so the paths really are absent and profiles
// really are being ignored. Certainty is what earns the strongest grade.
func TestTheOfficialNarrowImageIsFailedWithoutTheConfigPaths(t *testing.T) {
	args := without(without(recipeArgs(), "-e", true), "-e", true)
	rows := containerFindings(t, "docker", args)
	if worstOf(rows) != "fail" {
		t.Fatalf("a container with neither path variable was not failed:\n%s", joined(rows))
	}
	if !strings.Contains(joined(rows), "RTA_CONFIG") {
		t.Errorf("the finding did not name the variable, so it is not actionable:\n%s", joined(rows))
	}
}

// withImage swaps the image the recipe launches.
func withImage(args []string, image string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		if strings.Contains(a, "this-is-tobi/rta:") {
			out[i] = image
		}
	}
	return out
}

// **warn, not fail.** rta-full pre-trusts every bundled plugin at build time
// and reads need no grant, so its extra plugins are reachable with no consent
// step and the tool list to prompt-inject against is far larger. That is worth
// saying loudly. But every gate still works — grants, guard, record, locks —
// and in this report fail means a gate is off. Grading it the same as an
// unrestricted shell would be the report overstating, which is how a report
// stops being believed.
func TestTheFullImageIsAWarningRatherThanAFailure(t *testing.T) {
	rows := containerFindings(t, "docker",
		withImage(recipeArgs(), "ghcr.io/this-is-tobi/rta-full:latest"))
	var image []string
	for _, r := range rows {
		if strings.Contains(r[2], "rta-full") {
			image = r
		}
	}
	if image == nil {
		t.Fatalf("the batteries-included image produced no finding:\n%s", joined(rows))
	}
	if image[1] != "warn" {
		t.Errorf("rta-full graded %s, want warn: %s", image[1], strings.Join(image, " "))
	}
	// The row has to say why, not cite a page. Pre-trusted plugins plus free
	// reads is the mechanism; "the docs say not to" is not a finding.
	if !strings.Contains(image[2], "trust") && !strings.Contains(image[2], "grant") {
		t.Errorf("the row does not name the mechanism: %s", image[2])
	}
}

// rta-full sets RTA_CONFIG and RTA_DATA_DIR in the image itself, so a
// declaration that does not repeat them is correct. Failing it would be the
// audit reporting a problem it created by not knowing what it was looking at.
func TestTheFullImageIsNotFaultedForPathsItAlreadySets(t *testing.T) {
	args := withImage(recipeArgs(), "ghcr.io/this-is-tobi/rta-full:latest")
	args = without(without(args, "-e", true), "-e", true)
	// No row at all, not merely no failure: rta-full is known to set both, so
	// even an "if the image does not set them" note is noise about a question
	// this audit can answer.
	for _, r := range containerFindings(t, "docker", args) {
		if strings.Contains(r[2], "RTA_CONFIG") || strings.Contains(r[2], "RTA_DATA_DIR") {
			t.Errorf("an image known to set the paths was asked about them: %s",
				strings.Join(r, " "))
		}
	}
}

// **A private image is the ordinary case, not a suspect one.** The documented
// "share the image" recipe builds a derived image that sets RTA_CONFIG and
// RTA_DATA_DIR as ENV, and most private deployments will do the same. The
// audit cannot read a derived image's ENV without pulling it, which a config
// audit must not do — so it says what to check and does not accuse.
func TestACustomImageIsNotFailedForPathsItMayAlreadySet(t *testing.T) {
	args := without(without(withImage(recipeArgs(), "registry.internal/acme/rta-team:1.4.0"),
		"-e", true), "-e", true)
	rows := containerFindings(t, "docker", args)
	for _, r := range rows {
		if r[1] == "fail" {
			t.Errorf("a private image was failed: %s", strings.Join(r, " "))
		}
	}
	if !strings.Contains(joined(rows), "RTA_CONFIG") {
		t.Errorf("the audit said nothing at all about the paths:\n%s", joined(rows))
	}
}

// And a private image is never itself a finding: building your own is the
// documented way to narrow the plugin set, which is the thing being encouraged.
func TestACustomImageIsNotItselfAFinding(t *testing.T) {
	rows := containerFindings(t, "docker",
		withImage(recipeArgs(), "registry.internal/acme/rta-team:1.4.0"))
	if worstOf(rows) != "ok" {
		t.Errorf("a well-formed private deployment was graded %s:\n%s", worstOf(rows), joined(rows))
	}
}

// One decision, not three: "the server needs none of it, so it gets none of
// it". Three rows for one omission would be three-thirds of a finding.
func TestMissingHardeningIsOneWarningNamingAllOfIt(t *testing.T) {
	args := without(without(without(recipeArgs(), "--read-only", false), "--cap-drop", true),
		"--security-opt", true)
	rows := containerFindings(t, "docker", args)
	var hardening [][]string
	for _, r := range rows {
		if strings.Contains(r[2], "--read-only") || strings.Contains(r[2], "--cap-drop") {
			hardening = append(hardening, r)
		}
	}
	if len(hardening) != 1 {
		t.Fatalf("want exactly one hardening row, got %d:\n%s", len(hardening), joined(rows))
	}
	for _, want := range []string{"--read-only", "--cap-drop", "--security-opt"} {
		if !strings.Contains(hardening[0][2], want) {
			t.Errorf("the row did not name %s:\n%s", want, hardening[0][2])
		}
	}
	if hardening[0][1] != "warn" {
		t.Errorf("hardening graded %s, want warn", hardening[0][1])
	}
}

// The path root defaults to the working directory, which in a container is /
// unless something says otherwise.
func TestAContainerWithoutARootIsAWarning(t *testing.T) {
	rows := containerFindings(t, "docker", without(recipeArgs(), "--root", true))
	if !strings.Contains(joined(rows), "--root") || worstOf(rows) != "warn" {
		t.Errorf("a container with no path root was not warned about:\n%s", joined(rows))
	}
}

// Grants and the record have to outlive the container, or every restart is a
// machine with no memory of what you allowed.
func TestADataDirWithNoVolumeIsAWarning(t *testing.T) {
	args := make([]string, 0)
	src := recipeArgs()
	for i := 0; i < len(src); i++ {
		if src[i] == "-v" && strings.Contains(src[i+1], "/rta-home") {
			i++
			continue
		}
		args = append(args, src[i])
	}
	rows := containerFindings(t, "docker", args)
	if worstOf(rows) != "warn" || !strings.Contains(joined(rows), "/rta-home") {
		t.Errorf("an unmounted data dir was not warned about:\n%s", joined(rows))
	}
}

// **A bonus, not a baseline.** `--network none` turns off every capability
// that reaches the network, so most people running rta for what rta is for
// cannot use it — the recipe's own table says to drop it when you want those.
// An open network therefore earns no row at all, not even a muted one: a row
// on the ordinary correct setup reads as a deficiency and teaches people to
// skim the report.
func TestAnOpenNetworkEarnsNoRowAtAll(t *testing.T) {
	for _, r := range containerFindings(t, "docker", without(recipeArgs(), "--network", true)) {
		if strings.Contains(r[2], "network") {
			t.Errorf("an open network was reported: %s", strings.Join(r, " "))
		}
	}
}

// And closing it is worth confirming, because somebody chose it.
func TestAClosedNetworkIsConfirmed(t *testing.T) {
	var found bool
	for _, r := range containerFindings(t, "docker", recipeArgs()) {
		if strings.Contains(r[2], "network closed") {
			found = true
			if r[1] != "ok" {
				t.Errorf("a closed network graded %s, want ok", r[1])
			}
		}
	}
	if !found {
		t.Error("--network none was not confirmed anywhere")
	}
}

// A container that is not running rta is not this check's business, and
// inventing findings about somebody else's server is how a report stops being
// read.
func TestANonRtaContainerIsNotGraded(t *testing.T) {
	rows := containerFindings(t, "docker",
		[]string{"run", "--rm", "-i", "ghcr.io/someone/other-server:latest"})
	if len(rows) != 0 {
		t.Errorf("a container running something else produced findings:\n%s", joined(rows))
	}
}

// And neither is an rta that was never containerised: that is the shell
// question, already answered by the tools group, and repeating it here would
// put the same finding in two places with two wordings.
func TestAnUncontainerisedServerIsNotGraded(t *testing.T) {
	rows := containerFindings(t, "rta", []string{"mcp", "serve", "--as", "local"})
	if len(rows) != 0 {
		t.Errorf("a plain rta launch produced container findings:\n%s", joined(rows))
	}
}
