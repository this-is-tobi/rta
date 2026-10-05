package near

import "testing"

func TestWordNamesTheKeyATypoWasMeantFor(t *testing.T) {
	keys := []string{"output", "dashboard", "plugins", "profiles", "theme", "roles"}
	for _, c := range []struct{ typed, want string }{
		{"oputput", "output"},
		{"outpt", "output"},
		{"ouptut", "output"},
		{"plugin", "plugins"},
		{"dash", "dashboard"},
		{"them", "theme"},
		{"role", "roles"},
		{"OUTPUT", "output"},
		{"zzz", ""},
		{"out", "output"},
		{"x", ""},
	} {
		if got := Word(c.typed, keys); got != c.want {
			t.Errorf("Word(%q) = %q, want %q", c.typed, got, c.want)
		}
	}
}

// The tie goes to the first candidate, so a caller's order is its answer.
func TestWordPrefersTheFirstOfEquallyNearCandidates(t *testing.T) {
	if got := Word("cat", []string{"cut", "cap"}); got != "cut" {
		t.Errorf("got %q", got)
	}
	if got := Word("cat", []string{"cap", "cut"}); got != "cap" {
		t.Errorf("got %q", got)
	}
}

func TestWordPrefersTheNearestOverTheFirst(t *testing.T) {
	if got := Word("hiden", []string{"hiding", "hidden"}); got != "hidden" {
		t.Errorf("got %q", got)
	}
}

func TestDistanceCountsASwapOfNeighboursAsOneEdit(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"lsit", "list", 1}, {"abc", "abc", 0}, {"", "ab", 2}, {"colums", "columns", 1}} {
		if got := Distance(c.a, c.b); got != c.want {
			t.Errorf("Distance(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
