package match

import (
	"slices"
	"testing"
)

// The slice of the catalogue the search complaints came from, with the
// summaries as they are written there.
var catalogue = []Item{
	{ID: "cert.expiry", Summary: "Check certificate expiry for one or more hosts", Keywords: []string{"ssl", "tls"}},
	{ID: "fs.usage", Summary: "Show what is using space under a path, biggest first"},
	{ID: "kv.env", Summary: "Print stored values as shell exports", Keywords: []string{"secret"}},
	{ID: "kv.get", Summary: "Read a stored value", Keywords: []string{"secret"}},
	{ID: "net.hosts.list", Summary: "List the hosts file entries"},
	{ID: "net.port", Summary: "TCP connect-scan ports on a host"},
	{ID: "net.ping", Summary: "Ping a host and report latency statistics"},
	{ID: "note.add", Summary: "Add a note", Keywords: []string{"todo"}},
	{ID: "note.done", Summary: "Check a note off", Keywords: []string{"todo"}},
	{ID: "sys.cpu", Summary: "Show CPU model, core count and current usage"},
	{ID: "sys.disk", Summary: "Show disk usage per mounted filesystem"},
	{ID: "sys.ps", Summary: "List top processes by CPU or memory"},
	{ID: "pkg.self-update", Summary: "Update rta itself"},
}

func ids(items []Item, results []Result) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = items[r.Index].ID
	}
	return out
}

func TestAWordThatIsALeafOutranksAWordInSomeSummary(t *testing.T) {
	got := ids(catalogue, Find("ps", catalogue))
	if len(got) == 0 || got[0] != "sys.ps" {
		t.Errorf("ps finds %v, want sys.ps first", got)
	}
}

// `ports` ranked kv.env first because its summary says "exports"; the
// capability that scans ports was below it.
func TestAWordFoundInsideAnotherIsNotWhatLeadsTheResults(t *testing.T) {
	got := ids(catalogue, Find("ports", catalogue))
	if len(got) == 0 || got[0] != "net.port" {
		t.Fatalf("ports finds %v, want net.port first", got)
	}
	if i := slices.Index(got, "kv.env"); i >= 0 && i < slices.Index(got, "net.port") {
		t.Errorf("kv.env (exports) is above net.port in %v", got)
	}
}

func TestEveryWordOfAQueryHasToFindSomething(t *testing.T) {
	got := ids(catalogue, Find("hosts list", catalogue))
	if !slices.Equal(got, []string{"net.hosts.list"}) {
		t.Errorf("hosts list finds %v, want only net.hosts.list", got)
	}
	if got := Find("hosts nonsense", catalogue); len(got) != 0 {
		t.Errorf("a word nothing has still found %v", ids(catalogue, got))
	}
}

func TestKeywordsFindWhatTheIDAndSummaryDoNotSay(t *testing.T) {
	for query, want := range map[string]string{
		"todo": "note.add", "ssl": "cert.expiry", "secret": "kv.env",
	} {
		got := ids(catalogue, Find(query, catalogue))
		if !slices.Contains(got, want) {
			t.Errorf("%s finds %v, want %s among them", query, got, want)
		}
	}
}

func TestHyphensDotsAndSpacesAreTheSameSeparator(t *testing.T) {
	for _, q := range []string{"self-update", "self update", "pkg.self-update", "PKG self_update"} {
		got := ids(catalogue, Find(q, catalogue))
		if len(got) == 0 || got[0] != "pkg.self-update" {
			t.Errorf("%q finds %v, want pkg.self-update first", q, got)
		}
	}
}

func TestAPluralFindsTheSingular(t *testing.T) {
	got := ids(catalogue, Find("notes", catalogue))
	if len(got) < 2 || got[0] != "note.add" {
		t.Errorf("notes finds %v, want the note capabilities", got)
	}
	if got := Find("ps", []Item{{ID: "a.p", Summary: "x"}}); len(got) != 0 {
		t.Errorf("a short word lost its s and found %v", got)
	}
}

func TestADottedQueryIsAPlaceInTheTree(t *testing.T) {
	got := ids(catalogue, Find("net.hosts.l", catalogue))
	if len(got) == 0 || got[0] != "net.hosts.list" {
		t.Errorf("net.hosts.l finds %v, want net.hosts.list first", got)
	}
}

func TestNothingIsFoundByNothing(t *testing.T) {
	for _, q := range []string{"", "   ", "...", "-"} {
		if got := Find(q, catalogue); got != nil {
			t.Errorf("%q found %v", q, got)
		}
		if got := Nearest(q, catalogue); got != nil {
			t.Errorf("%q is near %v", q, got)
		}
	}
}

func TestEqualScoresKeepTheOrderTheyWereGivenIn(t *testing.T) {
	got := ids(catalogue, Find("kv", catalogue))
	if !slices.Equal(got, []string{"kv.env", "kv.get"}) {
		t.Errorf("kv finds %v, want them in catalogue order", got)
	}
}

func TestNearestAnswersWhatFindCannot(t *testing.T) {
	for query, want := range map[string]string{
		"disk space": "sys.disk",
		"sys.cpuu":   "sys.cpu",
		"proceses":   "sys.ps",
		"pings":      "net.ping",
		"cert.expir": "cert.expiry",
		"kvv":        "kv.get",
	} {
		got := ids(catalogue, Nearest(query, catalogue))
		if len(got) == 0 {
			t.Errorf("%q is near nothing, want %s", query, want)
			continue
		}
		if !slices.Contains(got[:min(3, len(got))], want) {
			t.Errorf("%q is nearest %v, want %s in the first three", query, got, want)
		}
	}
}

func TestNearestPrefersTheItemMoreOfTheWordsFind(t *testing.T) {
	got := ids(catalogue, Nearest("disk space", catalogue))
	if len(got) < 2 || got[0] != "sys.disk" || !slices.Contains(got, "fs.usage") {
		t.Errorf("disk space is near %v, want sys.disk then fs.usage", got)
	}
}

func TestDistanceCountsASwapAsOneEdit(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"list", "lsit", 1}, {"cpu", "cpuu", 1}, {"revoke", "reovke", 1}, {"abc", "abc", 0}, {"abc", "xyz", 3}} {
		if got := Distance(c.a, c.b); got != c.want {
			t.Errorf("Distance(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func BenchmarkFindOverACatalogue(b *testing.B) {
	items := make([]Item, 0, 400)
	for range 30 {
		items = append(items, catalogue...)
	}
	for b.Loop() {
		Find("disk usage", items)
	}
}
