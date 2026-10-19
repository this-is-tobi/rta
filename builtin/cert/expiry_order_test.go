package cert

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func expiryTable(t *testing.T, targets []string) view.Table {
	t.Helper()
	v, err := runExpiry(context.Background(), req(map[string]any{
		"targets": targets, "warn-days": 30, "timeout": "1s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	return v.(view.Table)
}

func targetsOf(t view.Table) []string {
	out := make([]string, len(t.Rows))
	for i, r := range t.Rows {
		out[i] = r[0]
	}
	return out
}

func served(t *testing.T, leaf time.Duration) string {
	t.Helper()
	chain, key := chainOf(t, leaf, 400*day, 400*day)
	return serveChain(t, chain, key)
}

// A tile shows the first rows of this table, so the first row is the one that
// needs somebody: a host that could not be checked, then the certificate that
// ends first, and the ones with a year left last — whatever order the hosts were
// listed in.
func TestExpiryRowsAreTheMostUrgentFirst(t *testing.T) {
	fine := served(t, 300*day)
	soon := served(t, 5*day)
	gone := served(t, -day)
	later := served(t, 20*day)
	down := blackhole(t)

	tbl := expiryTable(t, []string{fine, later, down, soon, gone})
	want := []string{down, gone, soon, later, fine}
	if got := targetsOf(tbl); !slices.Equal(got, want) {
		t.Errorf("rows are in the order %v, want %v", got, want)
	}
	for i, wantStatus := range []string{"ERROR", "EXPIRED", "WARN", "WARN", "ok"} {
		if got := tbl.Rows[i][3]; len(got) < len(wantStatus) || got[:len(wantStatus)] != wantStatus {
			t.Errorf("row %d (%s) status = %q, want it to lead with %q", i, tbl.Rows[i][0], got, wantStatus)
		}
	}
}

// Hosts that tie keep the order they were listed in: the failed ones, which
// have no date, and the ones that end the same day.
func TestExpiryRowsThatTieKeepTheOrderTheyWereListedIn(t *testing.T) {
	a, b, c := blackhole(t), blackhole(t), blackhole(t)
	if got, want := targetsOf(expiryTable(t, []string{b, a, c})), []string{b, a, c}; !slices.Equal(got, want) {
		t.Errorf("failed rows are in the order %v, want the order they were listed in: %v", got, want)
	}
}

// A certificate that is not valid yet fails every client as surely as an
// expired one, and its end date is far off: sorted by that date it would have
// been the last row of a tile that is cut.
func TestAnInvalidRowSortsWithTheFailuresAndNotByItsEndDate(t *testing.T) {
	results := []expiryResult{
		{cells: []string{"fine", "300d", "x", "ok"}, notAfter: time.Now().Add(300 * day)},
		{cells: []string{"early", "400d", "x", "INVALID — not valid until 2099-01-01"}, notAfter: time.Now().Add(400 * day), failing: true},
		{cells: []string{"soon", "5d", "x", "WARN <30d"}, notAfter: time.Now().Add(5 * day)},
		{cells: []string{"down", "-", "-", "ERROR: connecting"}, failing: true},
	}
	soonestFirst(results)
	var got []string
	for _, r := range results {
		got = append(got, r.cells[0])
	}
	if want := []string{"early", "down", "soon", "fine"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// How long is left leads, then the date: a tile that is too narrow for both
// drops the later column, and the one it keeps is the one that answers "how
// soon".
func TestExpiryColumnsPutHowLongBeforeTheDate(t *testing.T) {
	tbl := expiryTable(t, []string{served(t, 300*day)})
	var got []string
	kinds := map[string]view.ColumnKind{}
	for _, c := range tbl.Columns {
		got = append(got, c.Name)
		kinds[c.Name] = c.Kind
	}
	if want := []string{"Target", "In", "Expires", "Status"}; !slices.Equal(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
	if kinds["In"] != view.KindDuration || kinds["Expires"] != view.KindTimestamp || kinds["Status"] != view.KindStatus {
		t.Errorf("column kinds = %v", kinds)
	}
	row := tbl.Rows[0]
	if row[1] == "" || row[2] == "" || row[1] == row[2] {
		t.Errorf("row = %v: the second cell is the time left and the third the date", row)
	}
}

// The answer moves by the day and a run is a handshake with every host listed:
// a tile of it is paced at a floor an operator's file can lengthen and never
// shorten.
func TestExpiryTilesRunNoMoreOftenThanEveryTenMinutes(t *testing.T) {
	var c plugin.Capability
	for _, cc := range Plugin().Capabilities {
		if cc.ID == "cert.expiry" {
			c = cc
		}
	}
	if c.Refresh != 10*time.Minute {
		t.Errorf("cert.expiry declares Refresh %s, want 10m", c.Refresh)
	}
	if got, cut := plugin.ClampPace(c, 30*time.Second); got != 10*time.Minute || !cut {
		t.Errorf("a configured 30s pace = %s (cut: %v), want the floor", got, cut)
	}
}
