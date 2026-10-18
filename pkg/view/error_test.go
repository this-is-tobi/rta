package view

import "testing"

// **The rule is read from the codes that exist, not made up.** These are the
// codes the first-party plugins and the host name a refused credential with,
// and the ones they name a failure that is not one with: a timeout, a refused
// connection, an unreachable host and a bad input are the same answer on the
// next try, or a different one, and a tile that stopped asking after two of them
// would be hiding an outage. A code that is in neither list is a code the rule
// has not been shown, and pausing on it is a guess.
func TestACredentialRefusedIsNamedByTheCodesTheServicesUse(t *testing.T) {
	refused := []string{
		// The account was not accepted.
		"pg.auth.failed", "mysql.auth.failed", "mariadb.auth.failed", "redis.auth.failed",
		"etcd.auth.failed", "qdrant.auth.failed", "keycloak.auth.failed", "s3.auth.failed",
		// The account is known and may not do what was asked.
		"pg.denied", "mysql.denied", "mariadb.denied", "redis.denied", "etcd.denied", "qdrant.denied",
		"vault.denied", "s3.denied", "keycloak.denied", "docker.denied", "cnpg.denied",
		"mysql.database.denied", "mysql.host.denied", "etcd.snapshot.denied",
		"tunnel.denied", "tunnel.auth.denied", "tunnel.secret.denied",
	}
	for _, code := range refused {
		if e := Errorf(code, "x"); !e.CredentialRefused() {
			t.Errorf("%s is a refused credential and is not read as one", code)
		}
	}
	notRefused := []string{
		// The service did not answer, or not in time, or not with that.
		"pg.conn.refused", "pg.conn.timeout", "pg.conn.unreachable", "pg.conn.failed", "pg.host.unknown",
		"mysql.conn.refused", "redis.timeout", "redis.conn.closed", "etcd.timeout", "etcd.unavailable",
		"qdrant.timeout", "qdrant.ratelimited", "keycloak.timeout", "vault.conn.timeout", "vault.sealed",
		"s3.conn.timeout", "docker.unreachable", "cnpg.timeout", "kube.unreachable", "tui.refresh.timeout",
		"pkg.registry.ratelimited", "pkg.registry.unreachable",
		// The tile's own inputs, or a TLS answer, or a file.
		"pg.database.missing", "pg.tls.rejected", "pg.tls.required", "http.auth.file", "http.auth.twice",
		"redis.auth.unneeded", "net.port.resolve", "",
	}
	for _, code := range notRefused {
		if e := Errorf(code, "x"); e.CredentialRefused() {
			t.Errorf("%q is read as a refused credential, and the service did not refuse one", code)
		}
	}
}

// A policy gate says no to who is asking and not to a credential, whatever its
// code ends in; and an error that is not there is not a refusal.
func TestAPolicyRefusalIsNotACredentialRefused(t *testing.T) {
	if Refusef("core.grant.denied", "x").CredentialRefused() {
		t.Error("a policy refusal is read as a refused credential")
	}
	var none *Error
	if none.CredentialRefused() {
		t.Error("no error is read as a refused credential")
	}
}
