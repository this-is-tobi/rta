package grant

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/guard"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/internal/policy"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// envCatalog is a catalogue shaped like the plugins an environment configures:
// a database plugin with two connections in staging, a store that reveals
// values, and the kinds of capability a bundle must leave alone — one for the
// person at the terminal, one that takes no profile and needs no grant, one
// that takes no profile and does need one.
func envCatalog() []plugin.Capability {
	conn := []plugin.Field{{Name: "dsn", Type: plugin.String, Config: "dsn"}}
	return []plugin.Capability{
		{ID: "db.status", Safety: plugin.Read, Inputs: conn},
		{ID: "db.query", Safety: plugin.Read, Inputs: conn},
		{ID: "db.vacuum", Safety: plugin.Write, Inputs: conn},
		{ID: "db.drop", Safety: plugin.Destructive, Inputs: conn},
		{ID: "db.dump", Safety: plugin.Write, HumanOnly: true, Inputs: conn},
		{ID: "db.version", Safety: plugin.Read},
		{ID: "db.tool", Safety: plugin.Write},
		{ID: "vault.list", Safety: plugin.Read, Inputs: conn},
		{
			ID: "vault.get", Safety: plugin.Write, NeedsGrant: true, Reveals: true, Scope: "key",
			Inputs: append([]plugin.Field{{Name: "key", Type: plugin.String}}, conn...),
		},
		{ID: "net.probe", Safety: plugin.Read, Inputs: conn},
	}
}

const envConfig = `
profiles:
  staging:
    plugins:
      db:
        set: {dsn: main}
      db/analytics:
        set: {dsn: analytics}
      vault:
        set: {dsn: store}
`

// envSetup is roleSetup with the staging environment above in the config.
func envSetup(t *testing.T) (configDir, repo string) {
	t.Helper()
	configDir, repo = roleSetup(t)
	writeFile(t, filepath.Join(configDir, "config.yaml"), envConfig)
	return configDir, repo
}

func bundleHandler(req plugin.Request) (view.View, error) { return runIssue(req, envCatalog, builtIn) }

func bundleIssue(t *testing.T, values map[string]any) view.Sections {
	t.Helper()
	v, err := bundleHandler(issueReq(values, false, false))
	if err != nil {
		t.Fatalf("issue %v: %v", values, err)
	}
	return v.(view.Sections)
}

func bundleRefusal(t *testing.T, req plugin.Request) *view.Error {
	t.Helper()
	_, err := bundleHandler(req)
	var verr *view.Error
	if err == nil || !asError(err, &verr) {
		t.Fatalf("issue = %v, want a refusal", err)
	}
	return verr
}

// held is what stands, as target@connection, sorted.
func held(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, g := range standing(t) {
		out = append(out, g.Target+"@"+g.Profile)
	}
	slices.Sort(out)
	return out
}

func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()
	f()
	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

var readsOfStaging = []string{
	"db.query@staging", "db.query@staging/analytics", "db.status@staging", "db.status@staging/analytics",
	"vault.list@staging",
}

// **Reads only unless the person says more.** One short command whose reach is
// an environment has to start at the narrowest thing it can mean, and the
// receipt says which class it issued, so a person who typed no class still
// reads what they got.
func TestAnEnvironmentBundleIsReadsOnlyByDefault(t *testing.T) {
	envSetup(t)
	s := bundleIssue(t, map[string]any{"profile": "staging"})
	r := receipt(s)

	if got := held(t); !slices.Equal(got, readsOfStaging) {
		t.Fatalf("issued %q, want only the reads on every connection %q", got, readsOfStaging)
	}
	if got := pair(r, "environment"); got != "staging" {
		t.Errorf("environment = %q", got)
	}
	if got := pair(r, "agent"); got != "test" {
		t.Errorf("agent = %q", got)
	}
	if got := pair(r, "class"); !strings.HasPrefix(got, "reads — ") || strings.Contains(got, "and writes") {
		t.Errorf("class = %q, want it to say reads, and what a wider class adds", got)
	}
	for _, want := range []string{"`--safety write` 2 capabilities", "`--safety destructive` 1 capability more"} {
		if got := pair(r, "class"); !strings.Contains(got, want) {
			t.Errorf("class = %q, want it to say %q", got, want)
		}
	}
	if got := pair(r, "grants"); got != "5 grants, each for 12h" {
		t.Errorf("grants = %q", got)
	}
	if pair(r, "reveals") != "none" || pair(r, "destructive") != "none" {
		t.Errorf("reveals = %q, destructive = %q: a reads bundle reaches neither", pair(r, "reveals"), pair(r, "destructive"))
	}
	for _, g := range standing(t) {
		if g.Role != "" || g.Agent != "test" || g.ProfilePin == "" || g.Scope != "" {
			t.Errorf("grant %+v: an environment's row names a capability, a connection and an agent, and no role or record", g)
		}
		if want := 12 * time.Hour; g.Expires.Sub(g.Issued).Round(time.Minute) != want {
			t.Errorf("%s lasts %s, want %s", g.Target, g.Expires.Sub(g.Issued), want)
		}
	}
}

// --safety is a ceiling on class and never a target: write takes in the
// writes, a reveal among them, and destructive what destroys — and each
// names what it newly reaches.
func TestSafetyWidensTheBundleAClassAtATime(t *testing.T) {
	envSetup(t)
	s := bundleIssue(t, map[string]any{"profile": "staging", "safety": "write"})
	r := receipt(s)
	want := append(slices.Clone(readsOfStaging), "db.vacuum@staging", "db.vacuum@staging/analytics", "vault.get@staging")
	slices.Sort(want)
	if got := held(t); !slices.Equal(got, want) {
		t.Fatalf("write issued %q, want %q", got, want)
	}
	if got := pair(r, "reveals"); got != "vault.get — each for every record it reaches" {
		t.Errorf("reveals = %q: a capability that hands over a stored value is named, with what no record means", got)
	}
	if pair(r, "destructive") != "none" {
		t.Errorf("destructive = %q at the write class", pair(r, "destructive"))
	}
	if got := pair(r, "class"); !strings.HasPrefix(got, "reads and writes — `--safety destructive` 1 capability more") {
		t.Errorf("class = %q", got)
	}

	s = bundleIssue(t, map[string]any{"profile": "staging", "safety": "destructive"})
	r = receipt(s)
	want = append(want, "db.drop@staging", "db.drop@staging/analytics")
	slices.Sort(want)
	if got := held(t); !slices.Equal(got, want) {
		t.Fatalf("destructive issued %q, want %q", got, want)
	}
	if got := pair(r, "destructive"); got != "db.drop" {
		t.Errorf("destructive = %q, want every destructive capability by name", got)
	}
	if got := pair(r, "class"); got != "reads, writes and everything destructive" {
		t.Errorf("class = %q: nothing is left for a wider class to add", got)
	}
}

// An environment holding two connections to one plugin is enumerated
// connection by connection, each pinned to its own, and every connection is
// in the listing the passphrase is asked behind.
func TestAnEnvironmentWithTwoInstancesOfAPluginListsBoth(t *testing.T) {
	envSetup(t)
	var s view.Sections
	guardOn(t, "correct horse")
	unlock := unlockPrompted
	t.Cleanup(func() { unlockPrompted = unlock })
	unlockPrompted = func(plugin.Request) (guard.Signer, *view.Error) {
		return guard.Unlock("correct horse")
	}
	stderr := captureStderr(t, func() { s = bundleIssue(t, map[string]any{"profile": "staging"}) })

	if got := pair(receipt(s), "covers"); got != "db — 2 capabilities on staging, 2 on staging/analytics\nvault — 1 capability on staging" {
		t.Errorf("covers = %q", got)
	}
	for _, want := range []string{"db — 2 capabilities on staging, 2 on staging/analytics", "vault — 1 capability on staging"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the listing before the passphrase lacks %q:\n%s", want, stderr)
		}
	}
	pins := map[string]string{}
	for _, g := range standing(t) {
		if g.Target == "db.query" {
			pins[g.Profile] = g.ProfilePin
		}
	}
	if len(pins) != 2 || pins["staging"] == "" || pins["staging/analytics"] == "" || pins["staging"] == pins["staging/analytics"] {
		t.Errorf("db.query pins = %v, want one grant per connection, each pinned to its own", pins)
	}
	profiles := map[string]bool{}
	for _, row := range plan(s).Rows {
		profiles[row[2]] = true
	}
	if !profiles["staging"] || !profiles["staging/analytics"] {
		t.Errorf("the plan lists connections %v", profiles)
	}
}

// What the team's ceiling forbids is left out and named, and the rest is
// issued: nobody meant any one row, and refusing everything for one of them
// would make the ceiling a reason not to use the command.
func TestACapabilityTheCeilingForbidsIsSkippedAndNamed(t *testing.T) {
	_, repo := envSetup(t)
	teamPolicy(t, repo, "never: [db.query]\nneverProfile: [staging/analytics]\nrequireScope: [vault.get]\n")

	s := bundleIssue(t, map[string]any{"profile": "staging", "safety": "write"})
	r := receipt(s)
	want := []string{"db.status@staging", "db.vacuum@staging", "vault.list@staging"}
	if got := held(t); !slices.Equal(got, want) {
		t.Fatalf("issued %q, want %q: the rest of the bundle", got, want)
	}
	skipped := pair(r, "skipped by policy")
	for _, line := range []string{
		`"db.query" is not grantable here`,
		`no grant may name the "staging/analytics" connection`,
		`a grant on "vault.get" must name a record`,
	} {
		if !strings.Contains(skipped, line) {
			t.Errorf("skipped by policy lacks %q:\n%s", line, skipped)
		}
	}
	// Each by capability, and by connection where it was left out of some of
	// the ones the environment holds and not all.
	for _, name := range []string{"db.query", "db.status via staging/analytics", "db.vacuum via staging/analytics", "vault.get"} {
		if !strings.Contains(skipped, name) {
			t.Errorf("skipped by policy does not name %q:\n%s", name, skipped)
		}
	}
	if !strings.Contains(skipped, policy.RepoFile) {
		t.Errorf("skipped by policy names no file to edit:\n%s", skipped)
	}
}

// Refused only when nothing is left, and then with the ceiling's own words.
func TestABundleTheCeilingForbidsEntirelyIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"the environment":   "neverProfile: [staging]\n",
		"both its plugins":  "never: [db, vault]\n",
		"everything it has": "never: [db.query, db.status, vault.list]\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, repo := envSetup(t)
			teamPolicy(t, repo, body)
			verr := bundleRefusal(t, issueReq(map[string]any{"profile": "staging"}, false, false))
			if verr.Code != "grant.policy.refused" || !strings.Contains(verr.Message, "nothing on staging can be issued") ||
				verr.Hint != core.CeilingHint {
				t.Errorf("refusal = %+v, want the ceiling's, naming what it forbids", verr)
			}
			if len(standing(t)) != 0 {
				t.Errorf("a refused bundle left %q", held(t))
			}
		})
	}
}

// What cannot be a grant on a connection is left out and counted, and the part
// of it somebody may think the bundle covers is named.
func TestWhatCannotTakeAProfileIsLeftOutAndCounted(t *testing.T) {
	envSetup(t)
	r := receipt(bundleIssue(t, map[string]any{"profile": "staging", "safety": "destructive"}))
	left := pair(r, "left out")
	for _, want := range []string{
		"1 capability for the person at the terminal, never a tool",
		"2 capabilities that take no profile, so no grant on a connection names them",
		"db.tool needs a grant of its own",
	} {
		if !strings.Contains(left, want) {
			t.Errorf("left out lacks %q:\n%s", want, left)
		}
	}
	for _, g := range held(t) {
		for _, never := range []string{"db.dump", "db.version", "db.tool", "net.probe"} {
			if strings.HasPrefix(g, never+"@") {
				t.Errorf("%s was issued", g)
			}
		}
	}
}

// A plugin an environment configures that is not installed is said, not
// silently covered by nothing.
func TestAPluginTheEnvironmentNamesThatIsNotInstalledIsSaid(t *testing.T) {
	configDir, _ := envSetup(t)
	writeFile(t, filepath.Join(configDir, "config.yaml"), envConfig+"      ghost:\n        set: {dsn: x}\n")
	r := receipt(bundleIssue(t, map[string]any{"profile": "staging"}))
	if got := pair(r, "left out"); !strings.Contains(got, "staging configures ghost, which is not installed here") {
		t.Errorf("left out = %q", got)
	}
}

// One of a role and an environment, and each refusal says which it found.
func TestARoleOrAnEnvironmentButNotBothNorNeither(t *testing.T) {
	configDir, _ := envSetup(t)
	writeFile(t, filepath.Join(configDir, "config.yaml"), envConfig+"roles:\n  dev:\n    grants: [db.vacuum --profile staging]\n")

	neither := bundleRefusal(t, issueReq(map[string]any{}, false, false))
	if neither.Code != "grant.issue.nothing" || !strings.Contains(neither.Message, "a role, or --profile") {
		t.Errorf("neither = %+v", neither)
	}
	both := bundleRefusal(t, issueReq(map[string]any{"role": "dev", "profile": "staging"}, false, false))
	if both.Code != "grant.issue.both" || !strings.Contains(both.Message, "not both") {
		t.Errorf("both = %+v", both)
	}
	for _, flag := range []map[string]any{{"safety": "write"}, {"max-uses": 3}, {"rate": "10/1h"}, {"note": "why"}} {
		values := map[string]any{"role": "dev"}
		for k, v := range flag {
			values[k] = v
		}
		if verr := bundleRefusal(t, issueReq(values, false, false)); verr.Code != "grant.issue.bundleflag" {
			t.Errorf("%v with a role = %+v, want it refused: a role's lines carry their own", flag, verr)
		}
	}
	if len(standing(t)) != 0 {
		t.Errorf("a refusal left %q", held(t))
	}
}

// The hint of a flag given beside a role names a way that exists. --safety
// was told to be written on the role's line, which refuses it in turn
// (a line has no class), and every other flag was shown on a line of
// kv.get db-password, whether the role had a kv line or not.
func TestAFlagGivenBesideARoleIsHintedAtAWayThatExists(t *testing.T) {
	configDir, _ := envSetup(t)
	writeFile(t, filepath.Join(configDir, "config.yaml"), envConfig+"roles:\n  dev:\n    grants: [db.vacuum --profile staging]\n")

	safety := bundleRefusal(t, issueReq(map[string]any{"role": "dev", "safety": "write"}, false, false))
	if !strings.Contains(safety.Message, "names its capabilities itself") ||
		!strings.Contains(safety.Hint, "rta grant issue --profile <env> --safety write") ||
		strings.Contains(safety.Hint, "role's line") {
		t.Errorf("--safety beside a role = %q / %q, want the environment form and no line to write it on", safety.Message, safety.Hint)
	}
	for _, flag := range []map[string]any{{"max-uses": 3}, {"rate": "10/1h"}, {"note": "why"}} {
		values := map[string]any{"role": "dev"}
		for k, v := range flag {
			values[k] = v
		}
		verr := bundleRefusal(t, issueReq(values, false, false))
		if !strings.Contains(verr.Hint, "`db.vacuum --") || strings.Contains(verr.Hint, "kv.get db-password") {
			t.Errorf("%v beside a role is hinted %q, want a line of the role as the example", flag, verr.Hint)
		}
	}
}

func TestAnEnvironmentBundleRefusesWhatItCannotRead(t *testing.T) {
	envSetup(t)
	for name, c := range map[string]struct {
		values map[string]any
		code   string
	}{
		"one connection":   {map[string]any{"profile": "staging/analytics"}, "grant.issue.instance"},
		"no such profile":  {map[string]any{"profile": "prod"}, "grant.unknownprofile"},
		"an unknown class": {map[string]any{"profile": "staging", "safety": "everything"}, "grant.issue.safety"},
		"a bad window":     {map[string]any{"profile": "staging", "ttl": "soon"}, "grant.issue.ttl"},
		"a negative bound": {map[string]any{"profile": "staging", "max-uses": -1}, "grant.badmaxuses"},
		"a bad rate":       {map[string]any{"profile": "staging", "rate": "fast"}, "grant.badrate"},
	} {
		t.Run(name, func(t *testing.T) {
			if verr := bundleRefusal(t, issueReq(c.values, false, false)); verr.Code != c.code {
				t.Errorf("code = %s (%s), want %s", verr.Code, verr.Message, c.code)
			}
			if len(standing(t)) != 0 {
				t.Errorf("a refusal left %q", held(t))
			}
		})
	}
}

// What every row is built from — the agent, --max-uses, --rate — is refused
// as a grant's own are, once, and not against the first row generated: the
// refusal named `line "kube.cert.list --profile staging"`, which nobody wrote,
// for a flag the person typed on the command line. A role's agent is the same
// flag shared by every line.
func TestARefusedSharedFlagIsNotReportedAgainstARowNobodyWrote(t *testing.T) {
	configDir, _ := envSetup(t)
	writeFile(t, filepath.Join(configDir, "config.yaml"), envConfig+"roles:\n  dev:\n    grants: [db.vacuum --profile staging]\n")
	for name, c := range map[string]struct {
		values map[string]any
		code   string
	}{
		"a rate with no window":     {map[string]any{"profile": "staging", "agent": "claude", "rate": "5"}, "grant.badrate"},
		"a negative bound":          {map[string]any{"profile": "staging", "agent": "claude", "max-uses": -1}, "grant.badmaxuses"},
		"a name that is no name":    {map[string]any{"profile": "staging", "agent": "bad name"}, "grant.agent.charset"},
		"a role's name that is not": {map[string]any{"role": "dev", "agent": "bad name"}, "grant.agent.charset"},
	} {
		t.Run(name, func(t *testing.T) {
			verr := bundleRefusal(t, issueReq(c.values, false, false))
			if verr.Code != c.code || strings.Contains(verr.Message, "line \"") || strings.Contains(verr.Message, "environment \"") {
				t.Errorf("refused as %s: %s, want %s, and no line or environment it was found on", verr.Code, verr.Message, c.code)
			}
		})
	}
	// A line's own bound is still reported against the line that has it.
	writeFile(t, filepath.Join(configDir, "config.yaml"), envConfig+"roles:\n  dev:\n    grants: [db.vacuum --profile staging --rate 5]\n")
	if verr := bundleRefusal(t, issueReq(map[string]any{"role": "dev", "agent": "claude"}, false, false)); verr.Code != "grant.badrate" ||
		!strings.Contains(verr.Message, `role "dev", line "db.vacuum --profile staging --rate 5"`) {
		t.Errorf("a role line's own rate = %s: %s, want it reported against the line", verr.Code, verr.Message)
	}
}

// A connection the environment does not hold is not "one connection of" it:
// the wording belongs to a real one, and the hint of the wrong name offered a
// `grant allow` call that failed in turn. It is refused as `grant allow`
// refuses it, naming what the environment holds, whether the name is a
// misspelt label, a plugin where the label goes, or an environment that does
// not exist.
func TestAnUnknownConnectionOfAnEnvironmentIsRefusedLikeGrantAllowDoes(t *testing.T) {
	envSetup(t)
	for _, ref := range []string{"staging/nope", "staging/db", "staging/vault"} {
		verr := bundleRefusal(t, issueReq(map[string]any{"profile": ref}, false, false))
		if verr.Code != "grant.unknowninstance" || strings.Contains(verr.Message, "one connection of") ||
			verr.Hint != "it has: staging, staging/analytics" {
			t.Errorf("%s = %s / %q / %q, want the connections staging holds", ref, verr.Code, verr.Message, verr.Hint)
		}
	}
	if verr := bundleRefusal(t, issueReq(map[string]any{"profile": "prod/analytics"}, false, false)); verr.Code != "grant.unknownprofile" {
		t.Errorf("a connection of an environment that does not exist = %s, want grant.unknownprofile", verr.Code)
	}
}

// A grant that names a profile `profile list` calls invalid is issued, listed
// like a working one, and refused on every call — under the sentence an
// ungranted call is told, which says to issue the grant again. The person who
// issued it is told why instead, with the fix, whichever way it is issued: a
// single grant, an environment's bundle, or a draft prepared for an operator.
// A profile that is usable says nothing.
func TestAGrantOnAnUnusableProfileSaysWhyEveryCallIsRefused(t *testing.T) {
	configDir, _ := envSetup(t)
	writeFile(t, filepath.Join(configDir, "config.yaml"), envConfig+
		"  stale:\n    plugins:\n      db@000000000000:\n        set: {dsn: main}\n")
	cannot := "note: profile stale cannot be used, so every call through this grant is refused until it is fixed — " +
		`"db" is built in and has no artifact to pin (write it as ` + "`db:`)"
	allow := func(profile string) string {
		t.Helper()
		v, err := runAllow(context.Background(), issueReq(map[string]any{
			"target": "db.query", "profile": profile, "agent": "test", "ttl": "1h"}, false, false), envCatalog, builtIn)
		if err != nil {
			t.Fatalf("allow on %s: %v", profile, err)
		}
		return v.(view.Text).Body
	}
	if body := allow("stale"); !strings.Contains(body, cannot) {
		t.Errorf("the receipt of a grant on an unusable profile reads %q, want %q", body, cannot)
	}
	if body := allow("staging"); strings.Contains(body, "cannot be used") {
		t.Errorf("a usable profile is warned about: %q", body)
	}

	prepared, verr := PrepareRemote(envCatalog, builtIn)(operatorid.IssueSpec{
		Target: "db.query", Profile: "stale", Agent: "test", TTL: "1h"}, "alice")
	if verr != nil || !slices.Contains(prepared.Notes, cannot) {
		t.Errorf("the draft for an operator carries %q (%v), want %q", prepared.Notes, verr, cannot)
	}

	if got := pair(receipt(bundleIssue(t, map[string]any{"profile": "stale", "agent": "test"})), "note"); !strings.Contains(got, cannot) {
		t.Errorf("the receipt of a bundle on an unusable profile carries %q, want %q", got, cannot)
	}
}

// --max-uses and --rate are each row's own: the receipt says so beside the
// number, and every grant carries it.
func TestTheBoundsBindEachRowAndTheReceiptSaysSo(t *testing.T) {
	envSetup(t)
	r := receipt(bundleIssue(t, map[string]any{"profile": "staging", "max-uses": 3, "rate": "10/1h", "note": "triage", "ttl": "2h"}))
	want := "--max-uses 3 and --rate 10/1h bound each of the 5 grants on its own, not the bundle"
	if got := pair(r, "budget"); got != want {
		t.Errorf("budget = %q, want %q", got, want)
	}
	for _, g := range standing(t) {
		if g.MaxUses != 3 || g.RateMax != 10 || g.RateWindow != "1h" || g.Note != "triage" ||
			g.Expires.Sub(g.Issued).Round(time.Minute) != 2*time.Hour {
			t.Errorf("grant %+v does not carry the bounds each row was given", g)
		}
	}
}

func TestWithNoBoundTheReceiptStillSaysWhatTheyWouldBind(t *testing.T) {
	envSetup(t)
	got := pair(receipt(bundleIssue(t, map[string]any{"profile": "staging"})), "budget")
	if !strings.Contains(got, "neither is given") || !strings.Contains(got, "each grant on its own and never the bundle") {
		t.Errorf("budget = %q", got)
	}
}

// The ceiling's clamp is one fact about the whole bundle, said once.
func TestACappedWindowIsSaidOnceNotOncePerRow(t *testing.T) {
	_, repo := envSetup(t)
	teamPolicy(t, repo, "maxTTL: 1h\n")
	r := receipt(bundleIssue(t, map[string]any{"profile": "staging"}))
	if got := pair(r, "note"); strings.Count(got, "capped at 1h by your team's policy") != 1 {
		t.Errorf("note = %q, want the clamp said once", got)
	}
	for _, g := range standing(t) {
		if g.Expires.Sub(g.Issued).Round(time.Minute) != time.Hour {
			t.Errorf("%s lasts %s, want the ceiling's hour", g.Target, g.Expires.Sub(g.Issued))
		}
	}
}

// A dry run says what would be issued, with the facts the receipt gives, and
// writes nothing.
func TestADryRunOfABundleWritesNothing(t *testing.T) {
	envSetup(t)
	v, err := bundleHandler(issueReq(map[string]any{"profile": "staging", "safety": "write"}, true, false))
	if err != nil {
		t.Fatal(err)
	}
	s := v.(view.Sections)
	if got := pair(receipt(s), "grants"); got != "would issue 8 grants, each for 12h" {
		t.Errorf("grants = %q", got)
	}
	if len(plan(s).Rows) != 8 {
		t.Errorf("the plan lists %d rows, want every one of the 8", len(plan(s).Rows))
	}
	if len(standing(t)) != 0 {
		t.Fatalf("a dry run wrote %q", held(t))
	}
}

// A bundle issued over grants already standing says what it replaced, by where
// they came from — a hand grant and a role's alike — rather than claiming a
// refresh it cannot know.
func TestAReissuedBundleSaysWhatItReplaced(t *testing.T) {
	envSetup(t)
	bundleIssue(t, map[string]any{"profile": "staging"})
	if verr := core.Mutate(func(stored []core.Grant) ([]core.Grant, bool) {
		for i := range stored {
			if stored[i].Target == "db.status" && stored[i].Profile == "staging" {
				stored[i].Role = "dev"
			}
		}
		return stored, true
	}); verr != nil {
		t.Fatal(verr)
	}
	r := receipt(bundleIssue(t, map[string]any{"profile": "staging"}))
	if got := pair(r, "replaced"); got != "5 grants already standing for test — 1 under role dev, 4 under no role" {
		t.Errorf("replaced = %q", got)
	}
	if len(standing(t)) != 5 {
		t.Errorf("%d grants after issuing twice, want 5", len(standing(t)))
	}
}

// The receipt ends on the way back, the exact commands: the bundle carries no
// role, so the environment and the agent are what name it.
func TestTheReceiptEndsOnTheCommandsThatTakeTheBundleBack(t *testing.T) {
	envSetup(t)
	r := receipt(bundleIssue(t, map[string]any{"profile": "staging"}))
	last := r.Pairs[len(r.Pairs)-1]
	if last.Key != "take back" {
		t.Fatalf("the receipt ends on %q", last.Key)
	}
	for _, want := range []string{
		"`rta grant revoke --profile staging --agent test` takes back every grant test holds on staging, however it was issued",
		"`rta grant renew --profile staging --agent test` moves their deadline",
	} {
		if !strings.Contains(last.Value, want) {
			t.Errorf("take back = %q, want %q", last.Value, want)
		}
	}
}

// **Revoking by the environment and the agent takes the whole bundle back as
// one unit**, every connection of it, and nothing else of anybody's; renewing
// by the same two moves the whole bundle's deadline.
func TestTheBundleGoesBackAndMovesAsOneUnit(t *testing.T) {
	envSetup(t)
	bundleIssue(t, map[string]any{"profile": "staging", "safety": "write"})
	neighbour := func(target, profile, agent string) {
		t.Helper()
		g := core.Grant{Target: target, Profile: profile, Agent: agent, Issued: time.Now(), Expires: time.Now().Add(time.Hour), From: core.FromCommand}
		if verr := core.Issue(g, true); verr != nil {
			t.Fatal(verr)
		}
	}
	neighbour("db.query", "prod", "test")
	neighbour("vault.list", "staging", "other")
	if n := len(standing(t)); n != 10 {
		t.Fatalf("%d grants stand, want the bundle's 8 and two neighbours", n)
	}

	body := run(t, renewH, map[string]any{"profile": "staging", "agent": "test", "ttl": "20h"}).(view.Text).Body
	if !strings.HasPrefix(body, "renewed 8 grants") {
		t.Errorf("renew said %q, want the whole bundle", body)
	}
	for _, g := range standing(t) {
		moved := time.Until(g.Expires) > 19*time.Hour
		if want := g.Profile != "prod" && g.Agent == "test"; moved != want {
			t.Errorf("%s@%s for %s has %s left", g.Target, g.Profile, g.Agent, time.Until(g.Expires).Round(time.Minute))
		}
	}

	body = run(t, revokeH, map[string]any{"profile": "staging", "agent": "test"}).(view.Text).Body
	if !strings.HasPrefix(body, "revoked 8 grants") {
		t.Errorf("revoke said %q, want the whole bundle", body)
	}
	if got := held(t); !slices.Equal(got, []string{"db.query@prod", "vault.list@staging"}) {
		t.Errorf("after the revoke %q stand, want only the neighbours", got)
	}
}

func envRevokeH(ctx context.Context, req plugin.Request) (view.View, error) {
	return runRevoke(ctx, req, envCatalog)
}

// A bundle carries no id of its own, so a revoke of the environment and the
// agent printed a line to put back each grant and none naming the command that
// issued them all. It says that command once when the set revoked is exactly
// what the command issues — with the class, window, bound and note it was given
// — and every per-grant line carries the note, which `grant list` shows and
// they dropped. Anything less than the whole set, or a set somebody issued by
// hand, is not what that command would put back, and gets the lines alone.
func TestTheRevokeOfABundleNamesTheCallThatIssuedItAndKeepsTheNote(t *testing.T) {
	envSetup(t)
	bundleIssue(t, map[string]any{"profile": "staging", "agent": "test", "safety": "write", "ttl": "2h",
		"max-uses": 3, "rate": "10/1h", "note": "audit run"})
	body := run(t, envRevokeH, map[string]any{"profile": "staging", "agent": "test"}).(view.Text).Body
	issued := "rta grant issue --profile staging --agent test --safety write --ttl 2h --max-uses 3 --rate 10/1h --note 'audit run'"
	if !strings.Contains(body, "all of them are what `"+issued+"` issues: one call puts the set back, for a new window") {
		t.Errorf("the revoke of a whole bundle reads:\n%s\nwant the call that issued it", body)
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "re-issue: ") && !strings.Contains(line, "--note 'audit run'") {
			t.Errorf("a re-issue line drops the note: %s", line)
		}
	}
	if got := held(t); len(got) != 0 {
		t.Errorf("%q still stand", got)
	}

	// The reads of an environment are the default class, and need not be said.
	bundleIssue(t, map[string]any{"profile": "staging", "agent": "test"})
	body = run(t, envRevokeH, map[string]any{"profile": "staging", "agent": "test"}).(view.Text).Body
	if !strings.Contains(body, "`rta grant issue --profile staging --agent test --ttl 12h` issues") {
		t.Errorf("the revoke of a default bundle reads:\n%s\nwant the call without a class", body)
	}

	// Less than the whole set is not what the call would put back.
	bundleIssue(t, map[string]any{"profile": "staging", "agent": "test"})
	run(t, envRevokeH, map[string]any{"target": "db.status", "profile": "staging", "agent": "test"})
	body = run(t, envRevokeH, map[string]any{"profile": "staging", "agent": "test"}).(view.Text).Body
	if strings.Contains(body, "rta grant issue") {
		t.Errorf("a bundle with a grant gone is named as one:\n%s", body)
	}

	// Nor is a set issued by hand.
	for _, target := range []string{"db.status", "db.query"} {
		run(t, allowEnvH, map[string]any{"target": target, "profile": "staging", "agent": "test", "ttl": "1h"})
	}
	body = run(t, envRevokeH, map[string]any{"profile": "staging", "agent": "test"}).(view.Text).Body
	if !strings.HasPrefix(body, "revoked 2 grants") || strings.Contains(body, "rta grant issue") {
		t.Errorf("two grants issued by hand read as a bundle:\n%s", body)
	}
}

func allowEnvH(ctx context.Context, req plugin.Request) (view.View, error) {
	return runAllow(ctx, req, envCatalog, builtIn)
}

// The passphrase is asked for once however many grants are signed with it, and
// after the listing a person reads to decide; a refusal at the prompt writes
// nothing.
func TestOnePassphraseSignsTheWholeBundle(t *testing.T) {
	envSetup(t)
	guardOn(t, "correct horse")
	asked := 0
	unlock := unlockPrompted
	t.Cleanup(func() { unlockPrompted = unlock })
	unlockPrompted = func(plugin.Request) (guard.Signer, *view.Error) {
		asked++
		return guard.Unlock("correct horse")
	}

	stderr := captureStderr(t, func() { bundleIssue(t, map[string]any{"profile": "staging", "safety": "write"}) })
	if asked != 1 {
		t.Errorf("the passphrase was asked for %d times, want once for 8 grants", asked)
	}
	grants, verr := core.Load()
	if verr != nil {
		t.Fatalf("the signed bundle does not verify: %v", verr)
	}
	if len(grants) != 8 {
		t.Fatalf("%d grants, want 8", len(grants))
	}
	for _, g := range grants {
		if g.Sig == "" {
			t.Errorf("%s@%s is unsigned", g.Target, g.Profile)
		}
	}
	for _, want := range []string{"environment", "staging", "reads and writes", "vault.get — each for every record it reaches", "bound each grant"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the listing before the passphrase lacks %q:\n%s", want, stderr)
		}
	}

	// A role asks once too, and a refusal at the prompt writes nothing.
	configDir, _ := roleSetup(t)
	writeFile(t, filepath.Join(configDir, "config.yaml"), envConfig+"roles:\n  dev:\n    grants: [db.vacuum --profile staging, db.drop --profile staging]\n")
	guardOn(t, "correct horse")
	asked = 0
	captureStderr(t, func() {
		if _, err := bundleHandler(issueReq(map[string]any{"role": "dev"}, false, false)); err != nil {
			t.Fatal(err)
		}
	})
	if asked != 1 {
		t.Errorf("a role of two lines asked for the passphrase %d times, want once", asked)
	}

	envSetup(t)
	guardOn(t, "correct horse")
	unlockPrompted = func(plugin.Request) (guard.Signer, *view.Error) {
		return guard.Signer{}, view.Errorf("core.guard.passphrase", "that is not the guard's passphrase")
	}
	listed := captureStderr(t, func() {
		bundleRefusal(t, issueReq(map[string]any{"profile": "staging"}, false, false))
	})
	if len(standing(t)) != 0 {
		t.Errorf("a refused passphrase left %q", held(t))
	}
	if !strings.Contains(listed, "db — 2 capabilities on staging, 2 on staging/analytics") {
		t.Errorf("the refused passphrase came before the listing:\n%s", listed)
	}
}

// Through the form's masked field the same one value signs every row, and
// the file verifies on the next read.
func TestABundleIssuedFromAFormIsSignedByTheOneValue(t *testing.T) {
	envSetup(t)
	guardOn(t, "correct horse")
	tui := plugin.NewRequest(map[string]any{"profile": "staging", "passphrase": "correct horse"}, false, true).WithSurface(plugin.SurfaceTUI)
	if _, err := bundleHandler(tui); err != nil {
		t.Fatal(err)
	}
	grants, verr := core.Load()
	if verr != nil || len(grants) != 5 {
		t.Fatalf("%d grants, %v", len(grants), verr)
	}
	for _, g := range grants {
		if g.Sig == "" || g.From != core.FromForm {
			t.Errorf("%+v: want a signed grant from a form", g)
		}
	}
}

// **A bundle is local.** The operator channel signs one grant per envelope,
// and a bundle is read from the config and policy of the machine it is
// issued on, so `grant issue` has no --server: a flag it never took, as a
// role never did, refused as an unknown one, and the sentence in its help
// says why. A server asked for a grant on an environment and no capability
// has nothing to build.
func TestABundleCannotBeIssuedToARemoteServer(t *testing.T) {
	envSetup(t)
	for _, c := range Plugin(envCatalog, builtIn).Capabilities {
		if c.ID != "grant.issue" {
			continue
		}
		for _, f := range c.Inputs {
			if f.Name == "server" || f.Remote {
				t.Errorf("grant.issue takes %q, an input pointing it at another machine", f.Name)
			}
		}
		if !strings.Contains(c.Description, "Local only: the operator channel signs one grant per envelope") {
			t.Errorf("the help does not say a bundle is local: %q", c.Description)
		}
	}
	prepare := PrepareRemote(envCatalog, builtIn)
	_, verr := prepare(operatorid.IssueSpec{Profile: "staging", Agent: "test"}, "alice")
	if verr == nil || verr.Code != "grant.notarget" {
		t.Fatalf("a server asked to prepare an environment = %v, want it to find no capability named", verr)
	}
	if len(standing(t)) != 0 {
		t.Errorf("left %q", held(t))
	}
}

// Completion offers the environments with what each covers, and the class
// picker is the three classes.
func TestTheNewInputsCompleteSensibly(t *testing.T) {
	envSetup(t)
	issue := guardCapIn(t, "grant.issue")
	var profile, safety, role plugin.Field
	for _, f := range issue.Inputs {
		switch f.Name {
		case "profile":
			profile = f
		case "safety":
			safety = f
		case "role":
			role = f
		}
	}
	if role.Required {
		t.Error("the role is still required: it is one of two ways to say what to issue")
	}
	if got := profile.Candidates(context.Background(), req(nil)); !slices.Equal(got, []string{"staging\tdb, vault"}) {
		t.Errorf("--profile completes %q", got)
	}
	if !slices.Equal(safety.Options, []string{"read", "write", "destructive"}) {
		t.Errorf("--safety offers %q", safety.Options)
	}
}

func guardCapIn(t *testing.T, id string) plugin.Capability {
	t.Helper()
	for _, c := range Plugin(envCatalog, builtIn).Capabilities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no capability %q", id)
	return plugin.Capability{}
}

// A stale pin left beside its replacement names one connection twice, and the
// bundle issues it once and counts it once.
func TestAConnectionTheEnvironmentNamesTwiceIsOneRow(t *testing.T) {
	configDir, _ := envSetup(t)
	writeFile(t, filepath.Join(configDir, "config.yaml"), envConfig+"      vault@0123abcd:\n        set: {dsn: stale}\n")
	r := receipt(bundleIssue(t, map[string]any{"profile": "staging"}))
	if got := pair(r, "grants"); got != "5 grants, each for 12h" {
		t.Errorf("grants = %q, want the connection counted once", got)
	}
	if got := held(t); !slices.Equal(got, readsOfStaging) {
		t.Errorf("issued %q", got)
	}
}
