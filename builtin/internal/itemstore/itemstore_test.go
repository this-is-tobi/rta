package itemstore

import (
	"testing"
	"time"
)

func TestHasTagIsCaseAndHashInsensitive(t *testing.T) {
	it := Item{Tags: []string{"Backend", "urgent"}}
	for _, tag := range []string{"backend", "BACKEND", "#backend", "#Backend"} {
		if !it.HasTag(tag) {
			t.Errorf("HasTag(%q) = false, want true", tag)
		}
	}
	if it.HasTag("frontend") {
		t.Error("HasTag(frontend) = true, want false")
	}
}

func TestMatchesAcrossTitleBodyTags(t *testing.T) {
	it := Item{Title: "Fix login bug", Body: "affects the OAuth flow", Tags: []string{"backend"}}
	tests := []struct {
		query string
		want  bool
	}{
		{"", true},
		{"login", true},
		{"LOGIN", true},
		{"oauth", true},
		{"backend", true},
		{"login oauth", true}, // every term must match
		{"login frontend", false},
		{"nope", false},
	}
	for _, tt := range tests {
		if got := it.Matches(tt.query); got != tt.want {
			t.Errorf("Matches(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestReferencesExtractsUniqueIDsInOrder(t *testing.T) {
	got := References("see #12 for context, also #3 and #12 again, not a tag #abc")
	want := []int{12, 3}
	if len(got) != len(want) {
		t.Fatalf("References = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("References[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestReferencesEmpty(t *testing.T) {
	if got := References("nothing to see here"); len(got) != 0 {
		t.Errorf("References = %v, want empty", got)
	}
}

func TestParseDueShorthands(t *testing.T) {
	now := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC) // a Tuesday
	tests := []struct {
		in   string
		want string // 2006-01-02
	}{
		{"today", "2026-08-18"},
		{"Tomorrow", "2026-08-19"},
		{"next-week", "2026-08-25"},
		{"nextweek", "2026-08-25"},
		{"2026-09-01", "2026-09-01"},
		{"friday", "2026-08-21"},  // next Friday on/after Tuesday
		{"tuesday", "2026-08-18"}, // today counts as "next" tuesday
	}
	for _, tt := range tests {
		got, err := ParseDue(tt.in, now)
		if err != nil {
			t.Errorf("ParseDue(%q): %v", tt.in, err)
			continue
		}
		if got == nil || got.Format("2006-01-02") != tt.want {
			t.Errorf("ParseDue(%q) = %v, want %s", tt.in, got, tt.want)
		}
	}
}

func TestParseDueEmptyReturnsNil(t *testing.T) {
	got, err := ParseDue("", time.Now())
	if err != nil || got != nil {
		t.Errorf("ParseDue(\"\") = %v, %v; want nil, nil", got, err)
	}
}

func TestParseDueInvalidIsError(t *testing.T) {
	if _, err := ParseDue("whenever", time.Now()); err == nil {
		t.Error("ParseDue(whenever) should fail")
	}
}

func TestDueStatus(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	day := func(offset int) *time.Time {
		d := now.AddDate(0, 0, offset)
		return &d
	}
	tests := []struct {
		name string
		due  *time.Time
		done bool
		want string
	}{
		{"no due", nil, false, ""},
		{"done ignores overdue", day(-5), true, "done"},
		{"overdue", day(-1), false, "OVERDUE"},
		{"today", day(0), false, "WARN today"},
		{"soon", day(2), false, "WARN soon"},
		{"comfortable", day(10), false, "ok"},
	}
	for _, tt := range tests {
		if got := DueStatus(tt.due, tt.done, now); got != tt.want {
			t.Errorf("%s: DueStatus = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestChildrenAndProgress(t *testing.T) {
	s := Store{Items: []Item{
		{ID: 1, Title: "parent"},
		{ID: 2, Title: "child a", Parent: 1, Done: true},
		{ID: 3, Title: "child b", Parent: 1},
		{ID: 4, Title: "unrelated"},
	}}
	kids := Children(s, 1)
	if len(kids) != 2 {
		t.Fatalf("Children = %v", kids)
	}
	done, total := Progress(s, 1)
	if done != 1 || total != 2 {
		t.Errorf("Progress = %d/%d, want 1/2", done, total)
	}
	if _, total := Progress(s, 4); total != 0 {
		t.Errorf("leaf item should report 0 total, got %d", total)
	}
}

func TestPreviewCollapsesMarkdown(t *testing.T) {
	got := Preview("# Heading\n\n- item one\n- item two\n\n> quoted")
	want := "Heading · item one · item two · quoted"
	if got != want {
		t.Errorf("Preview = %q, want %q", got, want)
	}
}

func TestPreviewTruncatesLong(t *testing.T) {
	long := ""
	for i := 0; i < 20; i++ {
		long += "word "
	}
	got := Preview(long)
	if len([]rune(got)) > 60 {
		t.Errorf("Preview too long: %d runes", len([]rune(got)))
	}
}

func TestNormalizeTag(t *testing.T) {
	tests := map[string]string{
		"#Backend": "backend",
		"URGENT":   "urgent",
		" #api ":   "api",
	}
	for in, want := range tests {
		if got := NormalizeTag(in); got != want {
			t.Errorf("NormalizeTag(%q) = %q, want %q", in, got, want)
		}
	}
}
