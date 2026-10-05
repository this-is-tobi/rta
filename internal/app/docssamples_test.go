package app

import (
	"regexp"
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
