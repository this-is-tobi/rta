package app

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/view"
)

// grantRows is the detail of every "agent grants" row doctor writes: report
// keys its rows by check, and this check can write several.
func grantRows(t *testing.T) []string {
	t.Helper()
	tbl, ok := doctorReport(testRegistry(t)).(view.Table)
	if !ok {
		t.Fatal("doctor did not return a table")
	}
	var out []string
	for _, r := range tbl.Rows {
		if r[0] == "agent grants" {
			out = append(out, r[2])
		}
	}
	return out
}

// Doctor names a grant's record as the gate compares it, as every other
// listing does. It joined target and record and trimmed the pair, so a
// grant on a record padded with a no-break space — the one an answer given
// with --ttl to a padded call issues — read as the grant on the bare record,
// in the line saying what agents may do and in the one asking whether an
// unattended grant was yours.
func TestDoctorNamesAPaddedRecordAsTheGateComparesIt(t *testing.T) {
	isolate(t)
	padded := "db-password" + string(rune(0xa0))
	now := time.Now()
	if verr := grant.Save([]grant.Grant{
		{Target: "kv.get", Scope: "db-password", Issued: now, Expires: now.Add(15 * time.Minute)},
		{Target: "kv.get", Scope: padded, From: grant.FromCommand, Issued: now, Expires: now.Add(15 * time.Minute)},
	}); verr != nil {
		t.Fatal(verr)
	}
	rows := grantRows(t)
	if len(rows) < 2 {
		t.Fatalf("agent grants rows = %q, want the listing and the unattended line", rows)
	}
	shown := "kv.get " + strconv.QuoteToASCII(padded)
	// By what each says, not by position: the report is in the order of what
	// needs reading, so which of the two comes first is the status's to decide.
	var listing, unattended bool
	for _, r := range rows {
		listing = listing || strings.Contains(r, "kv.get db-password, "+shown)
		unattended = unattended || strings.Contains(r, ": "+shown+" — ")
	}
	if !listing {
		t.Errorf("no row lists both grants told apart, the padded one as %s: %q", shown, rows)
	}
	if !unattended {
		t.Errorf("no row asks about the unattended grant naming the padded one as %s: %q", shown, rows)
	}
	for _, r := range rows {
		if strings.ContainsRune(r, 0xa0) {
			t.Errorf("a row holds the no-break space itself: %q", r)
		}
	}
}

// A grant bound to a plugin build that no longer answers covers no call,
// and doctor says so with the fix, as it does for a changed connection:
// upgrading a plugin leaves every grant on it in this state, and the
// refusal an agent gets says only what an ungranted call is told.
//
// The test registry answers for demo itself, so a grant bound to a plugin
// artifact for demo is one whose build was replaced; nothing answers for
// gone at all.
func TestDoctorWarnsOfAGrantOnABuildThatNoLongerAnswers(t *testing.T) {
	isolate(t)
	now := time.Now()
	if verr := grant.Save([]grant.Grant{
		{Target: "demo.item.rm", Digest: "5dae737f8845c0ffee", Issued: now, Expires: now.Add(15 * time.Minute)},
		{Target: "gone.wipe", Scope: "db", Digest: "9f1c2e3d4b5a", Issued: now, Expires: now.Add(15 * time.Minute)},
		{Target: "demo.item.list", Issued: now, Expires: now.Add(15 * time.Minute)},
	}); verr != nil {
		t.Fatal(verr)
	}
	rows := strings.Join(grantRows(t), "\n")
	for _, want := range []string{
		"1 grant was issued on a plugin that has been replaced since, so it authorizes nothing: demo.item.rm — ",
		"`rta grant allow` issues it again",
		"1 grant names a plugin rta does not load now, so it authorizes nothing: gone.wipe db — ",
	} {
		if !strings.Contains(rows, want) {
			t.Errorf("doctor's grant rows do not say %q:\n%s", want, rows)
		}
	}
	if strings.Contains(rows, "demo.item.list —") || strings.Contains(rows, "nothing: demo.item.list") {
		t.Errorf("doctor warned of a built-in grant whose build answers:\n%s", rows)
	}
}
