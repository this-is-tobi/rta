package grant

import (
	"strconv"
	"testing"
	"time"
)

// A grant is named as the gate compares its record: the bare record as it
// is, one that does not read as itself quoted, and none at all as the
// target alone.
func TestNamedShowsTheRecordAsCompared(t *testing.T) {
	padded := "db" + string(rune(0xa0))
	for _, c := range []struct {
		g    Grant
		want string
	}{
		{Grant{Target: "kv.get"}, "kv.get"},
		{Grant{Target: "kv.get", Scope: "db"}, "kv.get db"},
		{Grant{Target: "kv.get", Scope: padded}, "kv.get " + strconv.QuoteToASCII(padded)},
		{Grant{Target: "kv.get", Scope: " db"}, `kv.get " db"`},
	} {
		if got := c.g.Named(); got != c.want {
			t.Errorf("Named(%q) = %q, want %q", c.g.Scope, got, c.want)
		}
	}
}

// Every record the roster can draw reads back as itself, and no two draw
// alike: what a row action seeds is the grant's own record. Among them the
// ones the roster's words collide with — none, a record named any, a folder
// and a record spelled like a folder's width — and each kind of character
// textclean.Record names by code point.
func TestARosterRecordReadsBackAsTheRecord(t *testing.T) {
	records := []string{
		"", "any", `"any"`, "any/", "db", "prod/", "prod/db", "my dir/", "prod/ (all)", "(all)",
		"db" + string(rune(0xa0)), " db", "db ", "a b", `"quoted`, `back\slash`, "tab\there",
		"line\nbreak", "\xff", "zw" + string(rune(0x200b)) + "sp", "blank" + string(rune(0x2800)),
		"prod/" + string(rune(0x200b)) + "/", string(rune(0x202e)) + "fdp.exe",
	}
	drawn := map[string]string{}
	for _, r := range records {
		cell := RosterRecord(r)
		if other, seen := drawn[cell]; seen {
			t.Errorf("%q and %q are both drawn as %q", other, r, cell)
		}
		drawn[cell] = r
		back, ok := RecordOfRoster(cell)
		if !ok || back != r {
			t.Errorf("RecordOfRoster(%q) = %q, %v; want %q back", cell, back, ok, r)
		}
	}
	// A cell the roster draws for no record reads as none of them.
	for _, cell := range []string{"prod/", "db (all)", "any (all)", `"unterminated`, `"db"`, `"prod/" (all)`, ""} {
		if back, ok := RecordOfRoster(cell); ok {
			t.Errorf("RecordOfRoster(%q) = %q, want it refused: no record is drawn so", cell, back)
		}
	}
}

// A connection reads back without the mark saying it changed, and the base
// connection's dash as none.
func TestARosterProfileReadsBackAsTheProfile(t *testing.T) {
	for _, c := range []struct {
		profile string
		changed bool
	}{{"", false}, {"", true}, {"staging", false}, {"staging", true}, {"staging/analytics", true}} {
		cell := RosterProfile(c.profile, c.changed)
		if back, ok := ProfileOfRoster(cell); !ok || back != c.profile {
			t.Errorf("ProfileOfRoster(%q) = %q, %v; want %q", cell, back, ok, c.profile)
		}
	}
	for _, cell := range []string{"", " (changed)"} {
		if back, ok := ProfileOfRoster(cell); ok {
			t.Errorf("ProfileOfRoster(%q) = %q, want it refused", cell, back)
		}
	}
}

// A grant's artifact is judged the way the gate compares it: exactly, in
// both directions, with nothing answering for the namespace its own case.
// And a listing agrees with the gate on every pairing — current exactly
// when the gate would let the grant cover a call.
func TestArtifactNowIsTheGatesComparison(t *testing.T) {
	const build, rebuilt = "5dae737f8845c0ffee", "9f1c2e3d4b5a60718"
	for _, c := range []struct {
		digest, current string
		known           bool
		want            ArtifactState
		cell            string
	}{
		{"", "", true, ArtifactCurrent, "built in"},
		{build, build, true, ArtifactCurrent, "5dae737f8845"},
		{build, rebuilt, true, ArtifactReplaced, "5dae737f8845 (replaced)"},
		{build, "", true, ArtifactReplaced, "5dae737f8845 (replaced)"},
		{"", build, true, ArtifactReplaced, "built in (replaced)"},
		{build, "", false, ArtifactGone, "5dae737f8845 (not loaded)"},
		{"", "", false, ArtifactGone, "built in (not loaded)"},
	} {
		g := Grant{Target: "hello.wipe", Agent: "a", Digest: c.digest, Issued: time.Now(), Expires: time.Now().Add(time.Hour)}
		got := g.ArtifactNow(c.current, c.known)
		if got != c.want {
			t.Errorf("digest %q against %q (known %v) = %v, want %v", c.digest, c.current, c.known, got, c.want)
		}
		if cell := RosterArtifact(c.digest, got); cell != c.cell {
			t.Errorf("digest %q against %q (known %v) is drawn %q, want %q", c.digest, c.current, c.known, cell, c.cell)
		}
		if c.known {
			covers := g.covers("hello.wipe", "", Caller{Agent: "a", Digest: c.current})
			if covers != (got == ArtifactCurrent) {
				t.Errorf("digest %q against %q: the gate covers = %v, the listing says %v", c.digest, c.current, covers, got)
			}
		}
	}
}
