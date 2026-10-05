package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A sample of what a command prints is the one thing in the docs a reader can
// check against their own terminal in seconds, and a sample that has drifted
// teaches them that the rest of the page may have too. The tests in this file
// hold a sample to the command that prints it, so a reworded row or a changed
// column fails here with the line to put in the page instead of waiting for a
// reader to notice.
//
// Only what does not depend on the machine is compared. A path, a host name
// or a clock reading differs everywhere, so a sample leaves those out or
// writes a placeholder, and what these tests read is the rest of the line.

// codeBlockAfter returns the first fenced block that follows marker in page,
// without its fence lines.
func codeBlockAfter(t *testing.T, page, marker string) []string {
	t.Helper()
	_, after, ok := strings.Cut(page, marker)
	if !ok {
		t.Fatalf("the page no longer carries %q; if the sample moved, move this test with it", marker)
	}
	_, block, ok := strings.Cut(after, "```\n")
	if !ok {
		t.Fatalf("no code block follows %q", marker)
	}
	block, _, _ = strings.Cut(block, "```")
	return strings.Split(strings.TrimRight(block, "\n"), "\n")
}

// realDoctorRows is doctor's report over the registry the binary ships, keyed
// by check name, as [status, detail]. The tests of doctor itself use a
// one-plugin registry, so its counts are not the ones a reader sees.
func realDoctorRows(t *testing.T) map[string][2]string {
	t.Helper()
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("building the built-in registry: %v", err)
	}
	tbl, ok := doctorReport(reg).(view.Table)
	if !ok {
		t.Fatal("doctor no longer returns a table")
	}
	rows := map[string][2]string{}
	for _, r := range tbl.Rows {
		rows[r[0]] = [2]string{r[1], r[2]}
	}
	return rows
}

var sampleRow = regexp.MustCompile(`^(\S.*?)\s{2,}(ok|info|warn|error)\s{2,}(\S.*)$`)

// The installation page shows what `rta doctor` says on a machine that has
// never run rta, and what its kv row says once a store unlocks from the
// environment. The sample they replace was written by hand from a machine in
// use — a store that unlocked, a dozen recorded calls — and presented as what
// a first `rta doctor` prints, which is where a new reader meets it.
func TestTheInstallationPageQuotesTheRowsDoctorPrintsOnAFreshMachine(t *testing.T) {
	const rel = "docs/10-getting-started/10-installation.md"
	page := readDoc(t, repoRoot(t), rel)

	isolate(t)
	fresh := realDoctorRows(t)
	quoted := 0
	for _, line := range codeBlockAfter(t, page, "these are the rows that matter, abridged") {
		m := sampleRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		quoted++
		got, ok := fresh[m[1]]
		if !ok {
			t.Errorf("%s quotes a %q row, and doctor has none by that name", rel, m[1])
			continue
		}
		if got[0] != m[2] || got[1] != m[3] {
			t.Errorf("%s quotes %q as %q %q, doctor prints %q %q", rel, m[1], m[2], m[3], got[0], got[1])
		}
	}
	if quoted < 4 {
		t.Fatalf("read %d rows out of the fresh-machine sample; has its shape changed?", quoted)
	}

	isolate(t)
	t.Setenv("RTA_KV_PASSPHRASE", "correct horse")
	writeStore(t)
	row := realDoctorRows(t)["kv store"]
	for _, line := range codeBlockAfter(t, page, "the `kv store` row changes to") {
		m := sampleRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		head, tail, _ := strings.Cut(m[3], " (<your key file>)")
		if row[0] != m[2] || !strings.HasPrefix(row[1], head) || !strings.HasSuffix(row[1], tail) {
			t.Errorf("%s quotes the kv row as %q %q, doctor prints %q %q", rel, m[2], m[3], row[0], row[1])
		}
	}
}

// The quickstart shows the first rows of the card `rta explain sys.cpu` prints
// and says the full card has more, so each row it quotes has to be a row the
// card has, spelled as the card spells it. The sample it replaced was the
// whole card, written by hand, and carried a config path that was one
// platform's.
func TestTheQuickstartQuotesRowsTheExplainCardHas(t *testing.T) {
	const rel = "docs/10-getting-started/20-quickstart.md"
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("building the built-in registry: %v", err)
	}
	card, _, err := run(t, reg, "explain", "sys.cpu")
	if err != nil {
		t.Fatalf("rta explain sys.cpu: %v", err)
	}
	printed := map[string]bool{}
	for _, line := range strings.Split(card, "\n") {
		printed[strings.Join(strings.Fields(line), " ")] = true
	}

	quoted := 0
	for _, line := range codeBlockAfter(t, readDoc(t, repoRoot(t), rel), "rta explain sys.cpu\n```") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		quoted++
		if !printed[strings.Join(strings.Fields(line), " ")] {
			t.Errorf("%s quotes %q, which is not a row of the card explain prints:\n%s", rel, line, card)
		}
	}
	if quoted < 4 {
		t.Fatalf("read %d rows of the quickstart's card; has its shape changed?", quoted)
	}
}

// The trees page shows `rta fs tree docs --depth 1` over a folder holding a
// directory with one entry and three files. The sample ended on a branch that
// continues, a corner rta draws only for the last entry, so a reader comparing
// it with their own listing found the page wrong in the one line that closes
// it. The directory is built here from the sample itself, each file the size
// its line names, and what rta prints for it has to be the sample, apart from
// the root's own path.
func TestTheTreesPageShowsWhatFsTreePrints(t *testing.T) {
	const rel = "docs/20-using/30-trees.md"
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("building the built-in registry: %v", err)
	}
	quoted := codeBlockAfter(t, readDoc(t, repoRoot(t), rel), "rta fs tree docs --depth 1\n```")

	dir := filepath.Join(t.TempDir(), "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := regexp.MustCompile(`^[├└]── (\S+?)(/)? (?:(\d+) entry|(\d+(?:\.\d+)?) KiB)$`)
	built := 0
	for _, line := range quoted[1:] {
		m := entry.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("%s: cannot read the tree line %q", rel, line)
		}
		built++
		if m[2] == "/" {
			if err := os.MkdirAll(filepath.Join(dir, m[1]), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, m[1], "entry.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		kib, _ := strconv.ParseFloat(m[4], 64)
		if err := os.WriteFile(filepath.Join(dir, m[1]), make([]byte, int(kib*1024+0.5)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if built < 3 {
		t.Fatalf("read %d entries out of the tree sample; has its shape changed?", built)
	}

	out, _, err := run(t, reg, "fs", "tree", dir, "--depth", "1")
	if err != nil {
		t.Fatalf("rta fs tree: %v", err)
	}
	printed := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(quoted) != len(printed) {
		t.Fatalf("%s quotes %d lines of the tree, rta prints %d:\n%s", rel, len(quoted), len(printed), out)
	}
	if !strings.HasPrefix(quoted[0], "docs/ ") || !strings.HasPrefix(printed[0], "docs/ ") {
		t.Errorf("the root line should be the folder and its path: quoted %q, printed %q", quoted[0], printed[0])
	}
	for i := 1; i < len(quoted); i++ {
		if quoted[i] != printed[i] {
			t.Errorf("%s quotes %q, rta prints %q", rel, quoted[i], printed[i])
		}
	}
}
