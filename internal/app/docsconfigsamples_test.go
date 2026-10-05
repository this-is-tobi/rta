package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The two pages that were written for the config commands and the first
// hardening pass quote what those commands print, and each sample is held to
// the command here, with the one thing that differs on every machine, a path
// or a clock, left out of the comparison.

func squash(line string) string { return strings.Join(strings.Fields(ansi.Strip(line)), " ") }

// printedLines is every non-empty line of what a command printed, spaces
// squeezed, so a sample's column alignment is not what is compared.
func printedLines(stdout, stderr string) map[string]bool {
	lines := map[string]bool{}
	for _, text := range []string{stdout, stderr} {
		for _, line := range strings.Split(text, "\n") {
			if s := squash(line); s != "" {
				lines[s] = true
			}
		}
	}
	return lines
}

func TestTheConfigPageQuotesWhatTheConfigCommandsPrint(t *testing.T) {
	const rel = "docs/20-using/22-your-config-file.md"
	page := readDoc(t, repoRoot(t), rel)
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("building the built-in registry: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")

	stdout, stderr, err := runConfigAt(t, reg, path, "config", "set", "plugins.http.timeout", "10")
	if err != nil {
		t.Fatalf("rta config set plugins.http.timeout 10: %v\n%s", err, stderr)
	}
	got := printedLines(stdout, stderr)
	quoted := 0
	for _, line := range codeBlockAfter(t, page, "`set` answers with what it wrote and the line that undoes it:") {
		want := squash(line)
		if want == "" {
			continue
		}
		quoted++
		if head, _, isPath := strings.Cut(want, " in /home/you/"); isPath {
			want = head + " in " + path
		}
		if !got[want] {
			t.Errorf("%s quotes %q, which `rta config set` did not print:\n%s", rel, line, stdout)
		}
	}
	if quoted < 2 {
		t.Fatalf("read %d lines of the set receipt; has its shape changed?", quoted)
	}

	_, stderr, err = runConfigAt(t, reg, path, "config", "set", "plugins.http.timeout", "10s")
	if err == nil {
		t.Fatal("rta config set plugins.http.timeout 10s was accepted")
	}
	got = printedLines("", stderr)
	for _, line := range codeBlockAfter(t, page, "rta config set plugins.http.timeout 10s\n```") {
		if want := squash(line); want != "" && !got[want] {
			t.Errorf("%s quotes %q, which the refusal did not print:\n%s", rel, line, stderr)
		}
	}

	typos := "dashbord:\n  columns: 2\noutput: jsno\ntheme:\n  primry: \"#ffffff\"\n"
	if err := os.WriteFile(path, []byte(typos), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ = runConfigAt(t, reg, path, "config", "check", "--output", "json")
	problems := map[string]bool{}
	for _, row := range jsonRows(t, stdout) {
		problems[squash(row[1]+" "+row[2])] = true
	}
	checked := 0
	for _, line := range codeBlockAfter(t, page, "rta config check\n```") {
		cells := strings.Split(line, "│")
		if len(cells) < 5 || strings.TrimSpace(cells[2]) == "" || squash(cells[1]) == "LINE" {
			continue
		}
		checked++
		want := squash(cells[2] + " " + cells[3])
		if !problems[want] && !hasProblemStarting(problems, want) {
			t.Errorf("%s quotes the problem %q, which `rta config check` did not report: %v", rel, want, problems)
		}
	}
	if checked < 3 {
		t.Fatalf("read %d problems out of the check sample; has its shape changed?", checked)
	}
}

// jsonRows is the rows of a table answered under -o json.
func jsonRows(t *testing.T, out string) [][]string {
	t.Helper()
	var table struct {
		Rows [][]string `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &table); err != nil {
		t.Fatalf("a table was expected, got %q: %v", out, err)
	}
	return table.Rows
}

// hasProblemStarting is true when a quoted problem is the start of one printed:
// the sample's table wraps a long sentence over two lines, and its second line
// is what is read as a row of its own.
func hasProblemStarting(problems map[string]bool, quoted string) bool {
	for p := range problems {
		if strings.HasPrefix(p, quoted) {
			return true
		}
	}
	return false
}

func TestTheHardeningPageQuotesWhatALockPrints(t *testing.T) {
	const rel = "docs/30-boundary/46-harden-in-five-minutes.md"
	page := readDoc(t, repoRoot(t), rel)
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("building the built-in registry: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	stdout, stderr, err := runConfigAt(t, reg, path, "lock", "add", "claude", "--ttl", "1m", "--note", "checking the brake")
	if err != nil {
		t.Fatalf("rta lock add claude: %v\n%s", err, stderr)
	}
	got := printedLines(stdout, stderr)
	quoted := 0
	for _, line := range codeBlockAfter(t, page, "rta lock rm claude\n```") {
		want := squash(line)
		if want == "" {
			continue
		}
		quoted++
		if strings.HasPrefix(want, "lifts itself ") {
			if !hasPrefixLine(got, "lifts itself ") {
				t.Errorf("%s quotes %q and the lock printed no `lifts itself` line:\n%s", rel, line, stdout)
			}
			continue
		}
		if !got[want] {
			t.Errorf("%s quotes %q, which `rta lock add` did not print:\n%s", rel, line, stdout)
		}
	}
	if quoted < 4 {
		t.Fatalf("read %d lines of the lock sample; has its shape changed?", quoted)
	}
}

func hasPrefixLine(lines map[string]bool, prefix string) bool {
	for l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}
