package main

import (
	"strings"
	"testing"
)

// Consistency, and what happens when the target is a replica.
//
// Everything here was verified against a real streaming pair — a primary and
// a hot standby built with pg_basebackup — rather than reasoned about. Three
// things came out of that which reading the documentation would not have
// settled: a parallel dump *does* work against a PostgreSQL 17 standby, the
// recovery conflict is straightforward to provoke and produces a message
// naming neither the dump nor the fix, and pg_dump's own output is
// translated, so matching English text in it is luck unless the locale is
// pinned.

// The exact stderr from a standby cancelling a dump, captured from the live
// pair rather than written from memory. Three lines, and **the last one is
// the useless one** — which is what the reporting rule exists for.
const conflictStderr = `pg_dump: error: Dumping the contents of table "t1" failed: PQgetResult() failed.
pg_dump: detail: Error message from server: ERROR:  canceling statement due to conflict with recovery
pg_dump: detail: The command was: COPY public.t1 (id, payload) TO stdout;
`

func TestAReplicaCancellingTheDumpIsNamedAsItself(t *testing.T) {
	verr := classifyDump(errStub{}, conflictStderr, reqFor(t, "pg.dump", map[string]any{}))
	if verr.Code != "pg.dump.replicaconflict" {
		t.Fatalf("code = %q, want pg.dump.replicaconflict", verr.Code)
	}
	// The message must carry the line that explains it, not the COPY
	// statement that happens to come last.
	if !strings.Contains(verr.Message, "conflict with recovery") {
		t.Errorf("message = %q, want the line naming the conflict", verr.Message)
	}
	if strings.Contains(verr.Message, "The command was") {
		t.Errorf("message = %q, reported the last line instead of the explaining one", verr.Message)
	}
	// Both real fixes, and the third option. Verified live: with
	// hot_standby_feedback on, the same dump under the same churn succeeds.
	for _, want := range []string{"hot_standby_feedback", "max_standby_streaming_delay", "primary"} {
		if !strings.Contains(verr.Hint, want) {
			t.Errorf("hint = %q, want it to name %s", verr.Hint, want)
		}
	}
}

// **The flag that must never be reached for.** A server that cannot export a
// snapshot makes --jobs impossible, and --no-synchronized-snapshots is the
// flag that would make it "work": each worker reads at its own moment, and
// the result restores without complaint into a state the database was never
// in. The answer is a serial dump, said out loud.
func TestASnapshotThatCannotBeSharedIsRefusedRatherThanDowngraded(t *testing.T) {
	stderr := "pg_dump: error: could not obtain lock on relation\n" +
		"pg_dump: detail: Error message from server: ERROR:  pg_export_snapshot is not supported\n"
	verr := classifyDump(errStub{}, stderr, reqFor(t, "pg.dump", map[string]any{}))
	if verr.Code != "pg.dump.nosnapshot" {
		t.Fatalf("code = %q, want pg.dump.nosnapshot", verr.Code)
	}
	if !strings.Contains(verr.Hint, "--jobs 1") {
		t.Errorf("hint = %q, want it to name the serial dump", verr.Hint)
	}
	if !strings.Contains(verr.Hint, "never existed") {
		t.Errorf("hint = %q, want it to say why the downgrade is refused", verr.Hint)
	}
}

// The negative that matters more than any of the above: the flag is not in
// argv, in any combination of the inputs. Asserted over the whole grid
// rather than at one point, because the way this gets added is as a
// well-meaning fallback somewhere else.
func TestTheConsistencyFlagIsNeverDropped(t *testing.T) {
	for _, format := range []string{"plain", "custom", "directory"} {
		for _, jobs := range []int{1, 2, 8} {
			for _, include := range []string{"all", "schema", "data"} {
				got := strings.Join(dumpArgs(reqFor(t, "pg.dump", map[string]any{
					"format": format, "jobs": jobs, "include": include,
				})), " ")
				if strings.Contains(got, "no-synchronized-snapshots") {
					t.Fatalf("format=%s jobs=%d include=%s: %s", format, jobs, include, got)
				}
			}
		}
	}
}

// **pg_dump is translated, and rta reads its output.** The live run of the
// conflict path came back in French — `pg_dump: détail : La commande était`
// — and matched only because the substring that matched was the server's
// message rather than pg_dump's own label. A classifier that works in one
// locale and degrades to "unrecognised failure" in another is worse than one
// that never worked.
func TestTheChildsMessagesArePinnedToOneLanguage(t *testing.T) {
	env := childEnv(reqFor(t, "pg.dump", map[string]any{}))
	var found bool
	for _, kv := range env {
		if kv == "LC_ALL=C" {
			found = true
		}
	}
	if !found {
		t.Errorf("LC_ALL is not pinned, so classifyDump matches text in whatever language "+
			"the operator's shell happens to be: %v", env)
	}
}

// The child gets what it needs to connect and no more — an unrelated token in
// the operator's shell is not something pg_dump could ever have printed.
func TestTheChildGetsAMinimalEnvironment(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "should-not-be-inherited")
	env := childEnv(reqFor(t, "pg.dump", map[string]any{"password": "hunter2"}))

	var sawPassword bool
	for _, kv := range env {
		if strings.HasPrefix(kv, "AWS_") {
			t.Errorf("the child inherited %q", kv)
		}
		if kv == "PGPASSWORD=hunter2" {
			sawPassword = true
		}
	}
	if !sawPassword {
		t.Error("the password did not reach the child, which is the one thing it needs")
	}
}

// pg_dump has no single-value connection-string argument the way the
// in-process driver's DSN does — PGSSLROOTCERT is the only way the same CA
// dsn() writes for pg.query reaches pg_dump too, so an operator's sslrootcert
// is not honoured for queries and silently ignored for backups.
func TestTheChildGetsSSLRootCert(t *testing.T) {
	env := childEnv(reqFor(t, "pg.dump", map[string]any{"sslrootcert": "/etc/rta/pg-ca.crt"}))
	var found bool
	for _, kv := range env {
		if kv == "PGSSLROOTCERT=/etc/rta/pg-ca.crt" {
			found = true
		}
	}
	if !found {
		t.Errorf("PGSSLROOTCERT did not reach the child: %v", env)
	}
}

// The receipt states the guarantee rather than leaving it implied, and the
// two forms are genuinely different mechanisms: one transaction, or one
// exported snapshot several connections join.
func TestTheReceiptStatesWhichConsistencyGuaranteeItHad(t *testing.T) {
	serial := consistencyOf(reqFor(t, "pg.dump", map[string]any{"jobs": 1}))
	if !strings.Contains(serial, "REPEATABLE READ") {
		t.Errorf("serial = %q", serial)
	}
	parallel := consistencyOf(reqFor(t, "pg.dump", map[string]any{
		"format": "directory", "jobs": 4,
	}))
	if !strings.Contains(parallel, "pg_export_snapshot") || !strings.Contains(parallel, "4") {
		t.Errorf("parallel = %q, want it to name the mechanism and the worker count", parallel)
	}
}

// A dump from a replica is as current as the replay lag, and the receipt is
// where somebody is looking when that matters.
func TestTheReceiptSaysWhenTheSourceWasAReplica(t *testing.T) {
	standby := source{role: "standby", version: 170011}.describe()
	if !strings.Contains(standby, "standby") || !strings.Contains(standby, "17.11") {
		t.Errorf("standby = %q", standby)
	}
	if !strings.Contains(standby, "replay lag") {
		t.Errorf("standby = %q, want it to say what a replica dump is current as of", standby)
	}
	primary := source{role: "primary", version: 170011}.describe()
	if strings.Contains(primary, "replay lag") {
		t.Errorf("primary = %q, want no replica caveat on a primary", primary)
	}
}

type errStub struct{}

func (errStub) Error() string { return "exit status 1" }
