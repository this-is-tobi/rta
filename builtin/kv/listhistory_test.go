package kv

import (
	"context"
	"testing"
)

// A listing says how many earlier values an entry still keeps, once any
// entry keeps one — the column that tells a rotated key from one that has
// only ever been set, without `kv show` on each.
func TestListCountsTheEarlierValuesAnEntryKeeps(t *testing.T) {
	setup(t)
	for _, kv := range []map[string]any{
		{"key": "rotated", "value": "one"},
		{"key": "rotated", "value": "two"},
		{"key": "rotated", "value": "three"},
		{"key": "fresh", "value": "only"},
	} {
		if _, err := runSet(context.Background(), req(kv, false)); err != nil {
			t.Fatal(err)
		}
	}
	tbl := listTable(t, req(nil, false))
	c := col(t, tbl, "History")
	got := map[string]string{}
	for _, r := range tbl.Rows {
		got[r[0]] = r[c]
	}
	if got["rotated"] != "2" || got["fresh"] != "" {
		t.Errorf("history column = %v, want 2 for the rotated key and nothing for the fresh one", got)
	}
}

// And no column at all while nothing has been replaced: a column of blanks
// would name a concept the listing has no use for yet.
func TestListHasNoHistoryColumnUntilSomethingWasReplaced(t *testing.T) {
	setup(t)
	if _, err := runSet(context.Background(), req(map[string]any{"key": "k", "value": "v"}, false)); err != nil {
		t.Fatal(err)
	}
	for _, c := range listTable(t, req(nil, false)).Columns {
		if c.Name == "History" {
			t.Fatal("a History column with nothing in it")
		}
	}
}
