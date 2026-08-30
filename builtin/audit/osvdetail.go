package audit

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// The second question to OSV: what are these advisories, actually.
//
// **The batch endpoint answers with identifiers and nothing else**, and a
// report that can only say "17 advisories name this package" leaves the
// reader with the whole triage still to do — which is high, which is
// theoretical, and what version makes them stop. That was recorded as a
// deliberate limit on the grounds that details are one request per advisory
// and this plugin does not crawl.
//
// The reasoning conflated two different crawls. The one worth refusing is
// proportional to **dependencies**: a project with nine hundred packages
// would ask nine hundred times, every run, most of them about something with
// no advisory at all. This one is proportional to **findings** — on a clean
// project it is zero extra requests, and a bad one is capped. That is a
// different shape of cost and it buys the part of the answer somebody acts
// on.
//
// It also fixes an over-count that the identifiers alone could not. OSV
// returns every database's record for the same vulnerability, so
// golang.org/x/net@0.5.0 comes back with twenty-five identifiers for about
// seventeen distinct issues: GHSA-vvpx-j8f3-3w6h and GO-2023-1571 are the
// same CVE under two names. Only the detail records carry `aliases`, so the
// count was wrong for as long as the details went unread.

const (
	osvVulnURL = "https://api.osv.dev/v1/vulns/"
	// osvDetailMax bounds one run's detail requests. A project with more
	// distinct advisories than this has a problem no report ranks its way out
	// of, and the cap is stated in the output rather than applied quietly.
	osvDetailMax = 60
	// osvDetailWorkers is the concurrency. Small on purpose: this is a public
	// endpoint nobody is paying for, and sixty requests at eight at a time is
	// under a second on any connection that could have run the batch query.
	osvDetailWorkers = 8
)

// osvRecord is the part of an OSV record this reads. Everything else in the
// schema — references, credits, the full affected matrix — is deliberately
// not decoded: what is not parsed cannot be misparsed, and none of it reaches
// the report.
type osvRecord struct {
	ID       string   `json:"id"`
	Aliases  []string `json:"aliases"`
	Summary  string   `json:"summary"`
	Severity []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	DatabaseSpecific struct {
		Severity string `json:"severity"`
	} `json:"database_specific"`
	Affected []struct {
		Package osvPackage `json:"package"`
		Ranges  []struct {
			Events []map[string]string `json:"events"`
		} `json:"ranges"`
	} `json:"affected"`
}

// severityRank orders the words, so "the worst one" is a comparison rather
// than a table of special cases. Zero is "no severity published", which sorts
// below low — an ungraded advisory is not a mild one, but it is the one to
// read last when others are graded.
var severityRank = map[string]int{"critical": 4, "high": 3, "medium": 2, "low": 1}

// severityOf grades one record, preferring what its own database published.
//
// The stated word wins over the computed score because it is the one the
// advisory's maintainers stand behind, and because GitHub — which publishes
// most of the records with a word at all — sometimes states a severity that
// its vector alone would not produce. The vector is the fallback that gives
// RustSec and the other vector-only databases a grade instead of a blank.
func severityOf(rec osvRecord) string {
	if word := strings.ToLower(strings.TrimSpace(rec.DatabaseSpecific.Severity)); severityRank[word] > 0 {
		return word
	}
	worst := ""
	for _, s := range rec.Severity {
		if !strings.HasPrefix(strings.ToUpper(s.Type), "CVSS_V3") {
			continue
		}
		score, ok := cvss3Score(s.Score)
		if !ok {
			continue
		}
		if word := cvssRating(score); severityRank[word] > severityRank[worst] {
			worst = word
		}
	}
	return worst
}

// fixedFor is the versions this record says fix the package that was asked
// about, in the order the record lists them.
//
// All of them, not the one that applies to the version in hand. An advisory
// often carries several — Go's net/http fix landed as both 1.19.6 and 1.20.1,
// one per release line — and choosing between them means comparing versions
// under whichever ordering that ecosystem uses, which is a different answer
// for Go, Maven, RubyGems and Debian. Naming all of them is true; naming the
// wrong one is worse than naming none.
func fixedFor(rec osvRecord, name, ecosystem string) []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range rec.Affected {
		if a.Package.Name != name || !strings.EqualFold(a.Package.Ecosystem, ecosystem) {
			continue
		}
		for _, rng := range a.Ranges {
			for _, ev := range rng.Events {
				if v := ev["fixed"]; v != "" && !seen[v] {
					seen[v] = true
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// detailOSV fetches the records for ids, concurrently and bounded, and
// reports whether it stopped short of the whole list.
//
// A record that cannot be fetched is simply absent: details are an
// enrichment, and a finding that already knows an advisory names this package
// must not become an error because a second request failed. The identifier is
// still reported, ungraded, which is exactly what this capability said before
// any of this existed.
func detailOSV(ctx context.Context, client *stdhttp.Client, ids []string) (map[string]osvRecord, bool) {
	return detailOSVAt(ctx, client, osvVulnURL, ids)
}

// detailOSVAt is detailOSV with the endpoint named, so both waves and the cap
// are testable against a server that answers slowly, partially or not at all —
// which is the interesting half of this, since every one of those has to leave
// a usable report rather than an error.
func detailOSVAt(ctx context.Context, client *stdhttp.Client, base string, ids []string) (map[string]osvRecord, bool) {
	sort.Strings(ids)
	capped := len(ids) > osvDetailMax
	if capped {
		ids = ids[:osvDetailMax]
	}
	records, stopped := fetchAll(ctx, client, base, ids, nil)

	// **A second wave, over the aliases of what came back ungraded.**
	//
	// Go's vulnerability database and PyPI's publish no severity at all, and
	// on a Go project that is most of the rows: golang.org/x/net@0.5.0 came
	// back seven graded out of eighteen, which is a severity column too blank
	// to triage with. Every one of those records names a GitHub advisory in
	// its aliases, and that one is graded — the two databases describe the
	// same vulnerability, so its grade is this vulnerability's grade.
	//
	// Only for the ungraded, only aliases not already in hand, and inside the
	// same budget: on a project where the first wave answered, this asks
	// nothing. The alias is used for the grade and never for the identifier —
	// the row still names and links what OSV said matches this package,
	// because an advisory OSV did not return for this version is not one to
	// put a reader's name on.
	if !stopped {
		var more []string
		for _, id := range ids {
			rec, ok := records[id]
			if !ok || severityOf(rec) != "" {
				continue
			}
			for _, alias := range rec.Aliases {
				// CVE identifiers included, which a first cut skipped on the
				// assumption that osv.dev only hosts database-native records. It
				// serves CVEs too, with the NVD's own CVSS vector on them, and
				// that vector is the only grade several Go advisories have
				// anywhere — seven of eighteen rows on the fixture this was
				// measured against.
				if _, have := records[alias]; !have {
					more = append(more, alias)
				}
			}
		}
		if budget := osvDetailMax - len(ids); budget > 0 && len(more) > 0 {
			sort.Strings(more)
			if len(more) > budget {
				more, capped = more[:budget], true
			}
			_, stopped = fetchAll(ctx, client, base, more, records)
		}
	}
	return records, capped || stopped
}

// fetchAll runs one bounded, concurrent wave into out, creating it when nil,
// and reports whether the context ended before every id was dispatched.
func fetchAll(ctx context.Context, client *stdhttp.Client, base string, ids []string,
	out map[string]osvRecord) (map[string]osvRecord, bool) {
	if out == nil {
		out = make(map[string]osvRecord, len(ids))
	}
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	work := make(chan string)
	for range min(osvDetailWorkers, len(ids)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range work {
				rec, ok := fetchOSVRecord(ctx, client, base+id)
				if !ok {
					continue
				}
				mu.Lock()
				out[id] = rec
				mu.Unlock()
			}
		}()
	}
	for _, id := range ids {
		select {
		case work <- id:
		case <-ctx.Done():
			// The deadline the caller set for the whole query. Stopping here
			// keeps whatever landed rather than discarding it, because a
			// partly-graded report is more useful than an ungraded one and
			// the counts below are of advisories, not of records read.
			close(work)
			wg.Wait()
			return out, true
		}
	}
	close(work)
	wg.Wait()
	return out, false
}

func fetchOSVRecord(ctx context.Context, client *stdhttp.Client, url string) (osvRecord, bool) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, url, nil)
	if err != nil {
		return osvRecord{}, false
	}
	req.Header.Set("User-Agent", "rta-audit/1")
	resp, err := client.Do(req)
	if err != nil {
		return osvRecord{}, false
	}
	defer resp.Body.Close() //nolint:errcheck // read-only
	if resp.StatusCode != stdhttp.StatusOK {
		return osvRecord{}, false
	}
	var rec osvRecord
	if json.NewDecoder(resp.Body).Decode(&rec) != nil {
		return osvRecord{}, false
	}
	return rec, true
}

// distinct collapses a package's identifiers onto one per underlying
// vulnerability, using the alias graph the detail records carry.
//
// Union-find rather than "mark this record's aliases as taken", because the
// alias relation is not reliably symmetric across databases: a GitHub
// advisory names the Go one and the Go one may not name it back. A one-way
// sweep would then keep both, which is the over-count this exists to remove.
//
// The representative is the graded identifier where there is one, and the
// first alphabetically otherwise — so the row somebody follows is the record
// that actually says how bad it is.
// vulnClass is one underlying vulnerability after its aliases are collapsed.
type vulnClass struct {
	// id is what the row names and links: always an identifier OSV returned
	// for this package, never an alias fetched only for its grade. An
	// advisory OSV did not return for this version is not one to put a
	// reader's name on.
	id string
	// severity is the best grade anywhere in the class, which is how a Go
	// record carrying none gets the one its GitHub alias publishes.
	severity string
	// rec is id's own record, and the one fixed versions come from — those
	// are a fact about this package in this ecosystem, so they have to come
	// from a record that names it.
	rec osvRecord
}

// classify collapses a package's identifiers onto one entry per underlying
// vulnerability, worst first.
//
// **OSV returns every database's record for the same issue**, so
// golang.org/x/net@0.5.0 comes back with twenty-five identifiers for eighteen
// distinct problems: GHSA-vvpx-j8f3-3w6h and GO-2023-1571 are one CVE under
// two names. Counting them separately inflates every number in the report,
// and only the detail records carry the `aliases` that say so.
//
// Union-find rather than "mark this record's aliases as taken", because the
// relation is not reliably symmetric across databases — a GitHub advisory
// names the Go one and the Go one may not name it back — and because it has
// to be transitive: GHSA→CVE and GO→CVE puts GHSA and GO in one class
// through an identifier neither of them mentions.
func classify(ids []string, records map[string]osvRecord) []vulnClass {
	uf := newUnionFind()
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	for _, id := range sorted {
		uf.add(id)
		for _, alias := range records[id].Aliases {
			uf.union(id, alias)
		}
	}

	// Grouped by root, keeping only the identifiers OSV actually returned:
	// an alias reached through the graph grades the class but never names it.
	groups := map[string][]string{}
	for _, id := range sorted {
		root := uf.find(id)
		groups[root] = append(groups[root], id)
	}

	out := make([]vulnClass, 0, len(groups))
	for _, members := range groups {
		c := vulnClass{id: members[0]}
		for _, id := range members {
			if better(id, c.id, records) {
				c.id = id
			}
			// The member's own grade, and its aliases' — the second wave
			// fetched those precisely because the member had none.
			for _, cand := range append([]string{id}, records[id].Aliases...) {
				if word := severityOf(records[cand]); severityRank[word] > severityRank[c.severity] {
					c.severity = word
				}
			}
		}
		c.rec = records[c.id]
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := severityRank[out[i].severity], severityRank[out[j].severity]; a != b {
			return a > b
		}
		return out[i].id < out[j].id
	})
	return out
}

// better prefers the identifier this run has a graded record for, then the
// higher grade, then the first alphabetically so a run is reproducible.
func better(a, b string, records map[string]osvRecord) bool {
	ra, hasA := records[a]
	rb, hasB := records[b]
	switch {
	case hasA != hasB:
		return hasA
	case !hasA:
		return a < b
	}
	if ga, gb := severityRank[severityOf(ra)], severityRank[severityOf(rb)]; ga != gb {
		return ga > gb
	}
	return a < b
}

// unionFind is the smallest one that does the job: path compression, no rank.
// The sets here are single digits.
type unionFind struct{ parent map[string]string }

func newUnionFind() *unionFind { return &unionFind{parent: map[string]string{}} }

func (u *unionFind) add(x string) string {
	if _, seen := u.parent[x]; !seen {
		u.parent[x] = x
	}
	return x
}

func (u *unionFind) find(x string) string {
	u.add(x)
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]]
		x = u.parent[x]
	}
	return x
}

func (u *unionFind) union(a, b string) {
	ra, rb := u.find(a), u.find(b)
	if ra != rb {
		u.parent[ra] = rb
	}
}

// advisoryLine is what a vulnerable package's row says after its name and
// version: how many distinct advisories, how they are graded, and the version
// that stops the worst of them.
//
// **The worst one is the whole point.** A package named in eighteen
// advisories where every one is low is a Friday-afternoon upgrade; the same
// count with one critical is not, and a list of identifiers could not tell
// those apart. The distribution follows so somebody can see whether it is one
// bad advisory or a pattern — including how many nobody graded, because a
// number that does not add up to the total is a number people stop trusting.
func advisoryLine(c component, classes []vulnClass) string {
	count := plural(len(classes), "advisory")
	if counts := gradeCounts(classes); counts != "" {
		count += " (" + counts + ")"
	}
	worst := classes[0]
	if worst.severity == "" {
		// Nothing here is graded, so there is no worst — the identifiers are
		// the whole answer, exactly as they were before any of this existed.
		return count + ": " + listIDs(classes)
	}

	// **Grade and remedy first, count second**, because the compact table
	// clips this cell at ninety-six characters and the first version put the
	// distribution in front — so the row said how many advisories there were
	// and was cut off exactly where it would have said how bad and what to
	// do. The identifier is not repeated here at all: the Link column already
	// carries it, and it is the longest thing that was competing for the
	// budget.
	line := worst.severity
	if fixed := fixedFor(worst.rec, c.name, c.ecosystem); len(fixed) > 0 {
		line += ", fixed in " + strings.Join(fixed, " or ")
	}
	return line + " — " + count
}

// listIDsMax bounds the identifier list on a wholly ungraded row. Eighteen of
// them is a wall rather than a fact, and the count before it already said how
// many there are.
const listIDsMax = 4

func listIDs(classes []vulnClass) string {
	ids := make([]string, 0, len(classes))
	for _, c := range classes {
		ids = append(ids, c.id)
	}
	if len(ids) <= listIDsMax {
		return strings.Join(ids, ", ")
	}
	return strings.Join(ids[:listIDsMax], ", ") +
		" and " + strconv.Itoa(len(ids)-listIDsMax) + " more"
}

// gradeCounts is the distribution, worst first, with the ungraded counted
// rather than dropped.
func gradeCounts(classes []vulnClass) string {
	counts := map[string]int{}
	for _, c := range classes {
		if c.severity == "" {
			counts["ungraded"]++
			continue
		}
		counts[c.severity]++
	}
	var parts []string
	for _, word := range []string{"critical", "high", "medium", "low", "ungraded"} {
		if n := counts[word]; n > 0 {
			parts = append(parts, strconv.Itoa(n)+" "+word)
		}
	}
	if len(parts) == 1 && counts["ungraded"] > 0 {
		return "" // "18 advisories (18 ungraded)" says nothing twice
	}
	return strings.Join(parts, ", ")
}
