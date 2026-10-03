package eol

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

const longHistory = `
	{"name":"5","releaseDate":"2025-01-01","isEol":false,"eolFrom":"2031-01-01","latest":{"name":"5.1"}},
	{"name":"4","releaseDate":"2023-01-01","isEol":false,"latest":{"name":"4.9"}},
	{"name":"3","releaseDate":"2021-01-01","isEol":true,"eolFrom":"2024-01-01","latest":{"name":"3.9"}},
	{"name":"2","releaseDate":"2019-01-01","isEol":true,"eolFrom":"2022-01-01","latest":{"name":"2.9"}},
	{"name":"1","releaseDate":"2017-01-01","isEol":true,"eolFrom":"2020-01-01","latest":{"name":"1.9"}}`

func cycleNamesOf(t *testing.T, v view.View) []string {
	t.Helper()
	var names []string
	for _, row := range v.(view.Table).Rows {
		names = append(names, row[0])
	}
	return names
}

// A product's table is read for what is still supported, and postgres is
// thirty-eight rows, most of them ended for years, with the three that matter
// scrolled off the screen. With no cycle named it lists what is supported and
// the most recent cycle that ended, which is how long ago a version lost its
// support; `all` is the whole table, and the footer counts it either way.
func TestCheckListsSupportedCyclesAndTheLatestEndedByDefault(t *testing.T) {
	srv := newEolServer(t, longHistory)

	v, err := runCheckAt(context.Background(), req(t, map[string]any{"product": "demo"}), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cycleNamesOf(t, v), " "); got != "5 4 3" {
		t.Errorf("cycles = %q, want the two supported and the latest ended: 5 4 3", got)
	}
	tbl := v.(view.Table)
	if tbl.Total != 5 {
		t.Errorf("total = %d, want all five counted", tbl.Total)
	}
	if len(tbl.Warnings) != 1 || !strings.Contains(tbl.Warnings[0].Message, "2 ended cycles not listed") ||
		!strings.Contains(tbl.Warnings[0].Hint, "all") {
		t.Errorf("warnings = %+v, want the two left out counted and `all` named", tbl.Warnings)
	}

	v, err = runCheckAt(context.Background(), req(t, map[string]any{"product": "demo", "all": true}), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cycleNamesOf(t, v), " "); got != "5 4 3 2 1" {
		t.Errorf("with all, cycles = %q, want every one", got)
	}
	if len(v.(view.Table).Warnings) != 0 {
		t.Errorf("with all, warnings = %+v, want none", v.(view.Table).Warnings)
	}

	v, err = runCheckAt(context.Background(), req(t, map[string]any{"product": "demo", "cycle": "1"}), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cycleNamesOf(t, v), " "); got != "1" {
		t.Errorf("a named ended cycle = %q, want it listed whatever the default", got)
	}
}
