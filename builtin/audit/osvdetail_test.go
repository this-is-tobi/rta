package audit

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// rec builds a record the way osv.dev serves one.
func rec(id string, aliases []string, word, vector string, affected ...string) osvRecord {
	r := osvRecord{ID: id, Aliases: aliases}
	r.DatabaseSpecific.Severity = word
	if vector != "" {
		r.Severity = append(r.Severity, struct {
			Type  string `json:"type"`
			Score string `json:"score"`
		}{Type: "CVSS_V3", Score: vector})
	}
	for i := 0; i+2 < len(affected)+1; i += 3 {
		a := struct {
			Package osvPackage `json:"package"`
			Ranges  []struct {
				Events []map[string]string `json:"events"`
			} `json:"ranges"`
		}{Package: osvPackage{Name: affected[i], Ecosystem: affected[i+1]}}
		a.Ranges = append(a.Ranges, struct {
			Events []map[string]string `json:"events"`
		}{Events: []map[string]string{{"introduced": "0"}, {"fixed": affected[i+2]}}})
		r.Affected = append(r.Affected, a)
	}
	return r
}

// **The stated word wins, and a vector is what a whole ecosystem has instead
// of one.** GitHub publishes a severity; RustSec publishes only a vector; Go
// and PyPI publish neither and leave it to their aliases. Reading only the
// stated word grades every Rust finding "unknown".
func TestSeverityPrefersTheStatedWordAndFallsBackToTheVector(t *testing.T) {
	high := "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H" // 7.5
	for _, tc := range []struct {
		what string
		in   osvRecord
		want string
	}{
		{"github states one", rec("GHSA-a", nil, "HIGH", high), "high"},
		{"rustsec states a vector only", rec("RUSTSEC-1", nil, "", high), "high"},
		{"go states neither", rec("GO-1", nil, "", ""), ""},
		{"a stated word that is not one falls through to the vector",
			rec("GHSA-b", nil, "MODERATE-ISH", high), "high"},
		{"an unreadable vector grades nothing rather than something",
			rec("GHSA-c", nil, "", "CVSS:4.0/AV:N/AC:L"), ""},
	} {
		t.Run(tc.what, func(t *testing.T) {
			if got := severityOf(tc.in); got != tc.want {
				t.Errorf("severityOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// Fixed versions are a fact about one package in one ecosystem, so a record
// naming three packages answers only for the one asked about.
func TestFixedVersionsComeFromTheMatchingPackageOnly(t *testing.T) {
	r := rec("GHSA-x", nil, "HIGH", "",
		"golang.org/x/net", "Go", "0.7.0",
		"lodash", "npm", "4.17.21")

	if got := fixedFor(r, "golang.org/x/net", "Go"); len(got) != 1 || got[0] != "0.7.0" {
		t.Errorf("Go fix = %v, want [0.7.0]", got)
	}
	if got := fixedFor(r, "lodash", "npm"); len(got) != 1 || got[0] != "4.17.21" {
		t.Errorf("npm fix = %v, want [4.17.21]", got)
	}
	if got := fixedFor(r, "golang.org/x/net", "npm"); len(got) != 0 {
		t.Errorf("a name in the wrong ecosystem answered %v", got)
	}
}

// **Twenty-five identifiers, eighteen problems.** OSV returns every
// database's record for the same vulnerability, and counting them separately
// inflates every number in the report.
func TestAliasesCollapseOntoOneVulnerability(t *testing.T) {
	records := map[string]osvRecord{
		"GHSA-1": rec("GHSA-1", []string{"CVE-1", "GO-1"}, "HIGH", ""),
		"GO-1":   rec("GO-1", []string{"CVE-1"}, "", ""),
		"GHSA-2": rec("GHSA-2", nil, "LOW", ""),
	}
	got := classify([]string{"GHSA-1", "GO-1", "GHSA-2"}, records)
	if len(got) != 2 {
		t.Fatalf("classify produced %d classes, want 2: %+v", len(got), got)
	}
	if got[0].id != "GHSA-1" || got[0].severity != "high" {
		t.Errorf("worst class = %+v, want GHSA-1 at high", got[0])
	}
	if got[1].id != "GHSA-2" {
		t.Errorf("second class = %q", got[1].id)
	}
}

// Transitively, and in both directions. The alias relation is not reliably
// symmetric across databases — a GitHub advisory names the Go one and the Go
// one may not name it back — and two records can be joined only through a
// third identifier neither of them mentions.
func TestTwoRecordsJoinThroughAnIdentifierNeitherNames(t *testing.T) {
	records := map[string]osvRecord{
		"GHSA-1": rec("GHSA-1", []string{"CVE-1"}, "HIGH", ""),
		"GO-1":   rec("GO-1", []string{"CVE-1"}, "", ""),
	}
	if got := classify([]string{"GHSA-1", "GO-1"}, records); len(got) != 1 {
		t.Errorf("classify produced %d classes, want 1 — both name CVE-1", len(got))
	}

	oneWay := map[string]osvRecord{
		"GHSA-2": rec("GHSA-2", []string{"GO-2"}, "HIGH", ""),
		"GO-2":   rec("GO-2", nil, "", ""), // does not name GHSA-2 back
	}
	if got := classify([]string{"GHSA-2", "GO-2"}, oneWay); len(got) != 1 {
		t.Errorf("a one-way alias left %d classes, want 1", len(got))
	}
}

// The class takes the grade its alias publishes, which is the whole reason
// the second wave exists: a Go record carries no severity and its GitHub or
// CVE alias does.
func TestAClassIsGradedByItsAlias(t *testing.T) {
	records := map[string]osvRecord{
		"GO-1":  rec("GO-1", []string{"CVE-1"}, "", ""),
		"CVE-1": rec("CVE-1", []string{"GO-1"}, "", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"),
	}
	// Only GO-1 was returned for this package; CVE-1 was fetched for its grade.
	got := classify([]string{"GO-1"}, records)
	if len(got) != 1 {
		t.Fatalf("classes = %d", len(got))
	}
	if got[0].severity != "critical" {
		t.Errorf("severity = %q, want critical from the alias", got[0].severity)
	}
	if got[0].id != "GO-1" {
		t.Errorf("id = %q — an advisory OSV did not return for this package must not name the row", got[0].id)
	}
}

// **Grade and remedy first.** The compact table clips this cell at
// ninety-six characters, and a line that led with the count was cut off
// exactly where it would have said how bad it is and what to do about it.
func TestTheLineLeadsWithWhatSomebodyActsOn(t *testing.T) {
	c := component{name: "golang.org/x/net", version: "v0.5.0", ecosystem: "Go"}
	records := map[string]osvRecord{
		"GHSA-1": rec("GHSA-1", nil, "CRITICAL", "", "golang.org/x/net", "Go", "0.55.0"),
		"GHSA-2": rec("GHSA-2", nil, "MEDIUM", ""),
		"GO-9":   rec("GO-9", nil, "", ""),
	}
	line := advisoryLine(c, classify([]string{"GHSA-1", "GHSA-2", "GO-9"}, records))

	if !strings.HasPrefix(line, "critical, fixed in 0.55.0") {
		t.Errorf("line = %q — the grade and the fix have to survive the clip", line)
	}
	if !strings.Contains(line, "3 advisories") {
		t.Errorf("line does not count them: %q", line)
	}
	// The ungraded are counted, not dropped: a distribution that does not add
	// up to the total is one people stop trusting.
	if !strings.Contains(line, "1 ungraded") {
		t.Errorf("line hides what nobody graded: %q", line)
	}
	if strings.Contains(line, "GHSA-1") {
		t.Errorf("the identifier is repeated in the cell the Link column already carries: %q", line)
	}
}

// With nothing graded at all the identifiers are the whole answer, exactly as
// they were before any of this existed — and bounded, because eighteen of
// them is a wall rather than a fact.
func TestAWhollyUngradedRowStillNamesItsAdvisories(t *testing.T) {
	c := component{name: "x", version: "1", ecosystem: "Go"}
	ids := []string{"GO-1", "GO-2", "GO-3", "GO-4", "GO-5", "GO-6"}
	records := map[string]osvRecord{}
	for _, id := range ids {
		records[id] = rec(id, nil, "", "")
	}
	line := advisoryLine(c, classify(ids, records))
	if !strings.Contains(line, "GO-1") || !strings.Contains(line, "and 2 more") {
		t.Errorf("line = %q, want the first few and a count of the rest", line)
	}
	if strings.Contains(line, "6 ungraded") {
		t.Errorf("line says the same thing twice: %q", line)
	}
}

// osvServer answers /<id> from a table and counts what was asked.
func osvServer(t *testing.T, table map[string]osvRecord) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		hits.Add(1)
		id := strings.TrimPrefix(r.URL.Path, "/")
		record, ok := table[id]
		if !ok {
			w.WriteHeader(stdhttp.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(record)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// The second wave asks only about the aliases of what came back ungraded —
// so a project whose first answers were graded pays for nothing extra.
func TestTheSecondWaveOnlyChasesWhatCameBackUngraded(t *testing.T) {
	table := map[string]osvRecord{
		"GHSA-1": rec("GHSA-1", []string{"CVE-1"}, "HIGH", ""),
		"GO-2":   rec("GO-2", []string{"CVE-2"}, "", ""),
		"CVE-2":  rec("CVE-2", []string{"GO-2"}, "", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H"),
	}
	srv, hits := osvServer(t, table)

	got, capped := detailOSVAt(context.Background(), srv.Client(), srv.URL+"/", []string{"GHSA-1", "GO-2"})
	if capped {
		t.Error("two identifiers reported as capped")
	}
	// GHSA-1 and GO-2, then CVE-2 for the ungraded one — and never CVE-1,
	// whose record was already graded.
	if n := hits.Load(); n != 3 {
		t.Errorf("%d requests, want 3 (two ids, one alias of the ungraded one)", n)
	}
	if _, chased := got["CVE-1"]; chased {
		t.Error("chased the alias of an advisory that was already graded")
	}
	if got["CVE-2"].ID != "CVE-2" {
		t.Error("the alias that carries the grade was not fetched")
	}
}

// A record that cannot be fetched is absent, never an error: the finding
// already knows this advisory names this package, and a second request
// failing must not turn a real result into no result.
func TestAFailedDetailLeavesTheFindingUngradedRatherThanBroken(t *testing.T) {
	srv, _ := osvServer(t, map[string]osvRecord{"GHSA-1": rec("GHSA-1", nil, "HIGH", "")})

	got, _ := detailOSVAt(context.Background(), srv.Client(), srv.URL+"/", []string{"GHSA-1", "GHSA-missing"})
	if len(got) != 1 || got["GHSA-1"].ID != "GHSA-1" {
		t.Fatalf("records = %v, want the one that answered", got)
	}
	classes := classify([]string{"GHSA-1", "GHSA-missing"}, got)
	if len(classes) != 2 {
		t.Errorf("classes = %d, want 2 — an unfetched advisory is still an advisory", len(classes))
	}
}

// Past the cap it stops and says so, because a blank severity that means "not
// asked" reads exactly like one that means "nobody published a grade".
func TestTheDetailPassIsCappedAndSaysSo(t *testing.T) {
	table := map[string]osvRecord{}
	ids := make([]string, 0, osvDetailMax+5)
	for i := range osvDetailMax + 5 {
		id := "GHSA-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		table[id] = rec(id, nil, "HIGH", "")
		ids = append(ids, id)
	}
	srv, hits := osvServer(t, table)

	got, capped := detailOSVAt(context.Background(), srv.Client(), srv.URL+"/", ids)
	if !capped {
		t.Error("a run past the cap did not report it")
	}
	if len(got) != osvDetailMax {
		t.Errorf("fetched %d records, want the cap of %d", len(got), osvDetailMax)
	}
	if n := hits.Load(); n != int64(osvDetailMax) {
		t.Errorf("%d requests, want %d", n, osvDetailMax)
	}
}

// A deadline the caller set keeps whatever landed rather than discarding it:
// a partly-graded report is more useful than an ungraded one, and it is
// reported as incomplete either way.
func TestADeadlineKeepsWhatLanded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv, _ := osvServer(t, map[string]osvRecord{})
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, capped := detailOSVAt(ctx, srv.Client(), srv.URL+"/", []string{"a", "b", "c"})
		if !capped {
			t.Error("a cancelled pass reported itself complete")
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled detail pass did not return")
	}
}
