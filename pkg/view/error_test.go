package view

import "testing"

// **The rule is read from the codes that exist, not made up.** Each case is a
// code the first-party plugins or the host return, under the reason it is or is
// not a refused credential, except the few marked constructed, which no code is
// today and which hold a precedence. The two endings are the whole of it: a code
// ending `.auth.failed` or `.denied` is a refusal, and everything else is not,
// however it is worded — a refusal in another word, a credential that is not
// there, a service that did not answer, one that answered with a limit, a file
// the operating system refused. A tile that stopped asking after a timeout would
// be hiding an outage, and one that kept asking after a refusal is how an account
// that locks gets locked.
func TestACredentialRefusedIsNamedByTheEndingsAndByNothingElse(t *testing.T) {
	cases := []struct {
		why     string
		code    string
		refusal bool
		want    bool
	}{
		// The account was not accepted.
		{"the account was not accepted", "pg.auth.failed", false, true},
		{"the account was not accepted", "mysql.auth.failed", false, true},
		{"the account was not accepted", "mariadb.auth.failed", false, true},
		{"the account was not accepted", "redis.auth.failed", false, true},
		{"the account was not accepted", "etcd.auth.failed", false, true},
		{"the account was not accepted", "qdrant.auth.failed", false, true},
		{"the account was not accepted", "keycloak.auth.failed", false, true},
		{"the account was not accepted", "s3.auth.failed", false, true},
		{"a login the cluster or the bastion did not accept", "tunnel.auth.failed", false, true},
		{"a login the cluster did not accept", "audit.kube.auth.failed", false, true},

		// The account is known and may not do what was asked.
		{"the account is known and may not do this", "pg.denied", false, true},
		{"the account is known and may not do this", "mysql.denied", false, true},
		{"the account is known and may not do this", "mariadb.denied", false, true},
		{"the account is known and may not do this", "redis.denied", false, true},
		{"the account is known and may not do this", "etcd.denied", false, true},
		{"the account is known and may not do this", "qdrant.denied", false, true},
		{"the account is known and may not do this", "vault.denied", false, true},
		{"the account is known and may not do this", "s3.denied", false, true},
		{"the account is known and may not do this", "keycloak.denied", false, true},
		{"the account is known and may not do this", "docker.denied", false, true},
		{"a finer refusal", "mysql.database.denied", false, true},
		{"a finer refusal", "mysql.host.denied", false, true},
		{"a finer refusal", "etcd.snapshot.denied", false, true},
		{"the cluster refused the audit its list", "audit.kube.denied", false, true},
		{"the cluster refused the forward", "tunnel.denied", false, true},
		{"the cluster refused the secret", "tunnel.secret.denied", false, true},
		{"the cluster refused the completion's list", "tunnel.list.denied", false, true},

		// A credential that is not there is not a refusal: the tile keeps asking, and
		// answers the moment the person has set it.
		{"the identity is not given", "kv.identity.required", false, false},
		{"the identity is locked", "kv.identity.locked", false, false},
		{"the passphrase is not given", "kv.passphrase.missing", false, false},
		{"the guard's passphrase is not given", "core.guard.passphrase.required", false, false},
		{"the credential plugin failed before any request", "tunnel.credential.missing", false, false},
		{"the credential plugin failed before any request", "audit.kube.credential.missing", false, false},
		{"the password is not given", "redis.auth.required", false, false},
		{"the tool that asks is not on the machine", "audit.kube.missing", false, false},

		// A refusal worded another way is not read, whatever the word: the rule is
		// the two endings, and a tile on one of these keeps asking. These are the
		// words the first-party plugins used before they took the two endings, kept
		// as constructed shapes since no code of theirs is worded so any more.
		{"a refusal worded forbidden", "thing.forbidden", false, false},
		{"a refusal worded unauthorized", "thing.unauthorized", false, false},
		{"a refusal worded unauthenticated", "thing.unauthenticated", false, false},

		// The service did not answer, or not in time.
		{"the host is unreachable", "pg.conn.unreachable", false, false},
		{"the host is unreachable", "docker.unreachable", false, false},
		{"the host is unreachable", "kube.unreachable", false, false},
		{"the host is unreachable", "cnpg.unreachable", false, false},
		{"the host is unreachable", "audit.kube.unreachable", false, false},
		{"the host is unreachable", "tunnel.cluster.unreachable", false, false},
		{"the host is unreachable", "tunnel.bastion.unreachable", false, false},
		{"the host is unreachable", "tunnel.dest.unreachable", false, false},
		{"the host is unreachable", "pkg.registry.unreachable", false, false},
		{"the connection timed out", "pg.timeout", false, false},
		{"the connection timed out", "mysql.timeout", false, false},
		{"the connection timed out", "mariadb.timeout", false, false},
		{"the connection timed out", "redis.timeout", false, false},
		{"the connection timed out", "etcd.timeout", false, false},
		{"the connection timed out", "qdrant.timeout", false, false},
		{"the connection timed out", "keycloak.timeout", false, false},
		{"the connection timed out", "vault.timeout", false, false},
		{"the connection timed out", "s3.timeout", false, false},
		{"the connection timed out", "audit.kube.timeout", false, false},
		{"the connection timed out", "tunnel.open.timeout", false, false},
		{"the connection timed out", "tunnel.secret.timeout", false, false},
		{"the connection timed out", "tui.refresh.timeout", false, false},
		{"the connection failed", "pg.conn.failed", false, false},
		{"the connection was refused", "pg.conn.refused", false, false},
		{"the connection was refused", "mysql.conn.refused", false, false},
		{"the connection dropped", "redis.conn.closed", false, false},
		{"the service is not ready", "etcd.unavailable", false, false},
		{"the service is sealed", "vault.sealed", false, false},

		// The service answered with a limit, which is a service that is there.
		{"a rate limit", "qdrant.ratelimited", false, false},
		{"a rate limit", "pkg.registry.ratelimited", false, false},

		// The operating system refused a file, which is no credential.
		{"a file the OS refused", "fs.tree.unreadable", false, false},
		{"a file the OS refused", "fs.hash.unreadable", false, false},
		{"a file the OS refused", "fs.usage.unreadable", false, false},
		{"a file the OS refused", "net.sysfile.permission", false, false},

		// The tile's own inputs, or a TLS answer.
		{"the database is not there", "pg.database.missing", false, false},
		{"a TLS answer", "pg.tls.rejected", false, false},
		{"a TLS answer", "pg.tls.required", false, false},
		{"an input", "http.auth.file", false, false},
		{"an input", "http.auth.twice", false, false},
		{"an input", "redis.auth.unneeded", false, false},
		{"a name that did not resolve", "pg.host.unknown", false, false},
		{"a name that did not resolve", "net.port.resolve", false, false},

		// What is not a code, and what only looks like the ending.
		{"no code", "", false, false},
		{"a bare word is not a code", "denied", false, false},
		// Constructed: no code puts a word after the ending.
		{"the ending is the last word", "pg.denied.soon", false, false},

		// A policy gate says no to who is asking, whatever its code ends in.
		{"a policy gate", "git.remote.mcp", true, false},
		// Constructed: no gate ends so today, and the precedence is what is held.
		{"a policy gate ending `.denied`", "git.remote.denied", true, false},
		{"a policy gate ending `.auth.failed`", "git.remote.auth.failed", true, false},
	}
	for _, tc := range cases {
		build := Errorf
		if tc.refusal {
			build = Refusef
		}
		if got := build(tc.code, "x").CredentialRefused(); got != tc.want {
			t.Errorf("%q (%s): read as a refused credential = %v, want %v", tc.code, tc.why, got, tc.want)
		}
	}
}

// An error that is not there is not a refusal.
func TestNoErrorIsNotACredentialRefused(t *testing.T) {
	var none *Error
	if none.CredentialRefused() {
		t.Error("no error is read as a refused credential")
	}
}
