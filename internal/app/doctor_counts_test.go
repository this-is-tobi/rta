package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/lockdown"
	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/policy"
)

// Every sentence doctor and the startup notice write around a count, at one
// and at many. A fixed "(s)" reads as a form letter on the one surface whose
// job is to be believed, and the words after the count — the verb, the
// pronoun that points back at it — are where a sentence that got the noun
// right still went wrong: "the 1 call before it were retired", "1 file is in
// the data directory is named like part of the record".
func TestDoctorsSentencesAgreeWithTheirCount(t *testing.T) {
	retired := time.Date(2026, 3, 4, 12, 0, 0, 0, time.Local)
	for _, tc := range []struct{ name, got, want string }{
		{"one store credential", storeCredentialsNote(1),
			"1 profile credential comes from the store, which unlocks from this environment — an " +
				"agent granted the profile that maps it causes that entry to be read (it never " +
				"receives the value, and reaches no entry a profile does not map)"},
		{"several store credentials", storeCredentialsNote(3),
			"3 profile credentials come from the store, which unlocks from this environment — an " +
				"agent granted a profile that maps one causes that entry to be read (it never " +
				"receives the value, and reaches no entry a profile does not map)"},
		{"a ceiling of one each", ceilingLimits(policy.Ceiling{
			Never: []string{"pg.dump"}, NeverProfile: []string{"prod"}, RequireScope: []string{"kv.get"},
		}), "1 target not grantable; 1 connection not grantable; 1 target must name a record"},
		{"a ceiling of several each", ceilingLimits(policy.Ceiling{
			Never: []string{"pg.dump", "s3"}, NeverProfile: []string{"prod", "staging"},
			RequireScope: []string{"kv.get", "vault.kv.get"},
		}), "2 targets not grantable; 2 connections not grantable; 2 targets must name a record"},
		{"one call waiting", waitingNote(1, 3*time.Minute),
			"1 call is waiting for you — it expires in 3m0s; `rta agent pending` lists it"},
		{"several calls waiting", waitingNote(2, 3*time.Minute),
			"2 calls are waiting for you — the next expires in 3m0s; `rta agent pending` lists them"},
		{"one tampered request", tamperedNote([]string{"a1"}),
			"1 request on the consent queue does not describe the call it is bound to — something " +
				"rewrote it after rta parked it, and it will not be offered or answered (a1)"},
		{"several tampered requests", tamperedNote([]string{"a1", "b2"}),
			"2 requests on the consent queue do not describe the calls they are bound to — something " +
				"rewrote them after rta parked them, and they will not be offered or answered (a1, b2)"},
		{"one call recorded, one retired", recordNote(agentlog.Report{
			Entries: 1, Files: 1, Retired: 1, RetiredAt: retired,
		}), "1 agent call recorded, chain intact; the 1 call before it was retired 2026-03-04"},
		{"several recorded, several retired", recordNote(agentlog.Report{
			Entries: 5, Files: 2, Size: 2048, Retired: 3, RetiredAt: retired,
		}), "5 agent calls recorded, chain intact across 2 files (2.0 KiB); " +
			"the 3 calls before it were retired 2026-03-04"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, tc.got, tc.want)
		}
	}
}

// The notice printed above an ordinary command, which is the one sentence
// here most people will read.
func TestTheUntrustedPluginNoticeAgreesWithItsCount(t *testing.T) {
	t.Cleanup(func() { SetUntrustedPlugins(nil) })
	digest := strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name  string
		found []pluginhost.Untrusted
		want  string
	}{
		{"one waiting", []pluginhost.Untrusted{{Name: "weather", Digest: digest}},
			"rta: 1 plugin installed and not run: weather — `rta plugin trust <name>` to load, " +
				"`rta plugin trust` to see it\n"},
		{"several waiting", []pluginhost.Untrusted{{Name: "weather", Digest: digest}, {Name: "tide", Digest: digest}},
			"rta: 2 plugins installed and not run: weather, tide — `rta plugin trust <name>` to load, " +
				"`rta plugin trust` to see them\n"},
		{"one colliding", []pluginhost.Untrusted{{Name: "kv", Digest: digest, Taken: true}},
			"rta: 1 artifact on $PATH names something already registered and was not run: kv — " +
				"trusting it would collide; remove or rename the file\n"},
		{"several colliding", []pluginhost.Untrusted{
			{Name: "kv", Digest: digest, Taken: true}, {Name: "git", Digest: digest, Taken: true},
		}, "rta: 2 artifacts on $PATH name something already registered and were not run: kv, git — " +
			"trusting one would collide; remove or rename the files\n"},
	} {
		SetUntrustedPlugins(tc.found)
		var out bytes.Buffer
		WarnUntrustedPlugins(&out, false)
		if out.String() != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, out.String(), tc.want)
		}
	}
}

// The rows doctor builds inline, read through doctor itself: a planted file
// named like a segment of the record, and a standing lock, one and then two.
func TestDoctorsRowsAgreeWithTheirCount(t *testing.T) {
	t.Run("files foreign to the record", func(t *testing.T) {
		dataDir, _ := isolate(t)
		plant := func(n int) {
			name := filepath.Join(dataDir, fmt.Sprintf("agent-log.%05d.jsonl", n))
			if err := os.WriteFile(name, []byte("not a record\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		plant(1)
		check(t, report(t), "agent log", "warn", "1 file in the data directory is named like "+
			"part of the record and was not written by rta, so it is excluded from it")
		plant(2)
		check(t, report(t), "agent log", "warn", "2 files in the data directory are named like "+
			"part of the record and were not written by rta, so they are excluded from it")
	})

	t.Run("standing locks", func(t *testing.T) {
		isolate(t)
		lock := func(name string) {
			l, verr := lockdown.Build("agent", name, "incident", "", "terminal")
			if verr != nil {
				t.Fatal(verr)
			}
			if verr := lockdown.Add(l); verr != nil {
				t.Fatal(verr)
			}
		}
		lock("claude")
		check(t, report(t), "locks", "info", "every call from it is refused")
		lock("codex")
		check(t, report(t), "locks", "info", "every call from each is refused")
	})
}
