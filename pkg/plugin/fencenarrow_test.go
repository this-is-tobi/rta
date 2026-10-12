package plugin

import (
	"context"
	"strings"
	"testing"
)

func joined(list []string) string { return strings.Join(list, ",") }

// A completion list is narrowed to the allowlist with each entry kept as it
// came, a description after a tab included: the description belongs to the
// shell's column and is dropped by whoever types the value.
func TestFenceFilterKeepsWhatItAllowsAsItCame(t *testing.T) {
	f := Field{Name: "namespace", Type: String, Config: "namespace"}
	fence := Fence{"namespace": {"staging", "staging-jobs"}}
	got := fence.Filter(f, []string{"prod\tlive", "staging\tthe shared one", "staging-jobs", "other"})
	if joined(got) != "staging\tthe shared one,staging-jobs" {
		t.Errorf("Filter = %q", got)
	}
	// Not named, not narrowed — and not fenced by an input with no config key.
	if got := fence.Filter(Field{Name: "other", Type: String, Config: "other"}, []string{"a", "b"}); joined(got) != "a,b" {
		t.Errorf("an input the fence does not name was narrowed: %q", got)
	}
	if got := fence.Filter(Field{Name: "namespace", Type: String}, []string{"a"}); joined(got) != "a" {
		t.Errorf("an input with no config key was narrowed: %q", got)
	}
}

// A completion entry is text whatever the input's type, so it is read as the
// value it spells before it is compared: `5432` offered for an integer is in a
// list of integers, and `5432.0` and `five` are not.
func TestFenceFilterReadsEntriesAsTheTypeTheInputDeclares(t *testing.T) {
	for _, tc := range []struct {
		name    string
		f       Field
		allowed []any
		entries []string
		want    string
	}{
		{"integers", Field{Type: Int}, []any{5432, uint64(5433)}, []string{"5432", "5433", "5434", "five", "5432.0"}, "5432,5433"},
		{"booleans", Field{Type: Bool}, []any{true}, []string{"true", "false", "yes", "1"}, "true"},
		{"durations", Field{Type: Duration}, []any{"30s"}, []string{"30s", "30000ms", "31s"}, "30s,30000ms"},
		{"a list is fenced a name at a time", Field{Type: StringSlice}, []any{"a", "b"}, []string{"a", "b", "c"}, "a,b"},
		{"options in another case", Field{Type: String, Options: []string{"fast", "safe"}}, []any{"safe"}, []string{"fast", "safe", "SAFE"}, "safe,SAFE"},
	} {
		tc.f.Name, tc.f.Config = "x", "x"
		got := Fence{"x": tc.allowed}.Filter(tc.f, tc.entries)
		if joined(got) != tc.want {
			t.Errorf("%s: Filter = %q, want %q", tc.name, joined(got), tc.want)
		}
	}
}

// An input with nothing to complete from of its own offers the allowlist, since
// the operator already wrote down what the connection reaches; one that has
// something offers that, narrowed.
func TestFenceNarrowOffersTheAllowlistWhereThereIsNothingElse(t *testing.T) {
	suggest := func(context.Context, Request) []string { return []string{"alpha", "beta"} }
	fence := Fence{"x": {"beta", "delta"}}
	for _, tc := range []struct {
		name     string
		f        Field
		declared []string
		want     string
	}{
		{"no Suggest", Field{Type: String}, nil, "beta,delta"},
		{"a Suggest narrowed", Field{Type: String, Suggest: suggest}, []string{"alpha", "beta"}, "beta"},
		{"a Suggest that answered nothing", Field{Type: String, Suggest: suggest}, nil, ""},
		// A Live Suggest never answers on this channel, so the allowlist is the answer.
		{"a Live Suggest", Field{Type: String, Suggest: suggest, Live: true}, nil, "beta,delta"},
		{"a closed set narrowed", Field{Type: String, Options: []string{"beta", "gamma"}}, []string{"beta", "gamma"}, "beta"},
	} {
		tc.f.Name, tc.f.Config = "x", "x"
		if got := fence.Narrow(tc.f, tc.declared); joined(got) != tc.want {
			t.Errorf("%s: Narrow = %q, want %q", tc.name, joined(got), tc.want)
		}
	}
	// Unfenced, a list is whole and an empty one stays empty.
	plain := Field{Name: "y", Type: String, Config: "y"}
	if got := fence.Narrow(plain, []string{"a"}); joined(got) != "a" {
		t.Errorf("an unfenced list was narrowed: %q", got)
	}
	if got := fence.Narrow(plain, nil); len(got) != 0 {
		t.Errorf("an unfenced input with nothing to offer offered %q", got)
	}
	if (Fence)(nil).Fenced(plain) || (Fence)(nil).Offer(plain) != nil {
		t.Error("no fence at all fenced an input")
	}
}
