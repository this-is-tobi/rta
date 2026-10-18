package plugin

import (
	"strings"
	"testing"
)

// A group is a heading over a block of boxes and nothing else. A name that
// cannot be drawn as one row, or that is not the lowercase word the others are,
// is refused where the author is.
func TestAGroupIsAShortLowercasePrintableName(t *testing.T) {
	for _, tc := range []struct {
		group   string
		wantSub string
	}{
		{"", ""},
		{"network", ""},
		{"tls", ""},
		{"read replica", ""},
		{"a", ""},
		{strings.Repeat("a", 24), ""},
		{strings.Repeat("a", 25), "24"},
		{"Network", "lowercase"},
		{" network", "space"},
		{"network ", "space"},
		{"net\twork", "printable"},
		{"net\nwork", "printable"},
		{"réseau", "printable"},
		{"net\x1b[31mwork", "printable"},
		{"net\x7fwork", "printable"},
	} {
		p := validPlugin()
		p.Capabilities[0].Inputs = append(p.Capabilities[0].Inputs,
			Field{Name: "host", Type: String, Group: tc.group})
		err := p.Validate()
		switch {
		case tc.wantSub == "" && err != nil:
			t.Errorf("group %q refused: %v", tc.group, err)
		case tc.wantSub != "" && (err == nil || !strings.Contains(err.Error(), tc.wantSub)):
			t.Errorf("group %q: err = %v, want %q", tc.group, err, tc.wantSub)
		}
	}
}

// A heading changes nothing about who may supply the input, so a credential can
// be headed like any other box.
func TestAGroupOnASecretInputIsAccepted(t *testing.T) {
	p := validPlugin()
	p.Capabilities[0].Inputs = append(p.Capabilities[0].Inputs,
		Field{Name: "password", Type: Secret, Local: true, EnvFallback: true, Group: "credentials"})
	if err := p.Validate(); err != nil {
		t.Errorf("a grouped secret is refused: %v", err)
	}
}

func connected(inputs ...Field) Capability {
	return Capability{ID: "svc.get", Inputs: inputs}
}

// The inputs of a connection, as a database plugin declares them: a Local input
// with a config key, and the credential the environment supplies.
func serviceInputs() []Field {
	return []Field{
		{Name: "host", Type: String, Local: true, Config: "host"},
		{Name: "port", Type: Int, Local: true, Config: "port"},
		{Name: "user", Type: String, Local: true, Config: "user"},
		{Name: "password", Type: Secret, Local: true, EnvFallback: true},
		{Name: "database", Type: String, Local: true, Config: "database"},
		{Name: "sslmode", Type: String, Local: true, Config: "sslmode"},
		{Name: "limit", Type: Int, Config: "limit"},
		{Name: "table", Type: String, Required: true},
	}
}

func TestTheConnectionIsWhatTheOperatorStatesAndTheCredentialBesideIt(t *testing.T) {
	c := connected(serviceInputs()...)
	for name, want := range map[string]bool{
		"host": true, "port": true, "user": true, "password": true, "database": true, "sslmode": true,
		// Config-backed and not Local: the call's own input with a default from the config.
		"limit": false,
		// Nothing says an operator can set it.
		"table": false,
	} {
		for _, f := range c.Inputs {
			if f.Name == name && Connection(c, f) != want {
				t.Errorf("Connection(%s) = %v, want %v", name, !want, want)
			}
		}
	}

	// A store's passphrase is Local and the environment can fill it, and no config states a
	// connection beside it: there is none.
	store := connected(
		Field{Name: "key", Type: String, Required: true},
		Field{Name: "passphrase", Type: Secret, Local: true, EnvFallback: true},
	)
	for _, f := range store.Inputs {
		if Connection(store, f) {
			t.Errorf("Connection(%s) on a capability whose config states no setting", f.Name)
		}
	}
}

// A plugin released before the field existed has no group declared, and a form
// reads as grouped anyway: the group of a connection input is read off its name
// and its type.
func TestAConnectionInputWithNoGroupDeclaredFallsBackToTheOneItsNameSays(t *testing.T) {
	for name, want := range map[string]string{
		"host": "network", "port": "network", "address": "network", "url": "network",
		"socket": "network", "endpoint": "network", "context": "network", "region": "network",
		"user": "credentials", "username": "credentials", "password": "credentials", "token": "credentials",
		"access-key": "credentials", "secret-key": "credentials", "client-id": "credentials",
		"client-secret": "credentials",
		"sslmode":       "tls", "tls": "tls", "tls-server-name": "tls", "ca-file": "tls", "cert-file": "tls",
		"key-file": "tls", "sslcert": "tls", "sslkey": "tls", "sslrootcert": "tls", "ssl-home": "tls",
		"client-cert": "tls",
		"database":    "target", "realm": "target", "mount": "target", "namespace": "target", "db": "target",
		"insecure": "target",
	} {
		c := connected(Field{Name: name, Type: String, Local: true, Config: name})
		if got := Group(c, c.Inputs[0]); got != want {
			t.Errorf("Group(%s) = %q, want %q", name, got, want)
		}
	}

	// A credential is a credential whatever it is called.
	secret := connected(Field{Name: "x", Type: String, Local: true, Config: "x"},
		Field{Name: "session-blob", Type: Secret, Local: true, EnvFallback: true})
	if got := Group(secret, secret.Inputs[1]); got != "credentials" {
		t.Errorf("a Secret named session-blob is in %q, want credentials", got)
	}
	slice := connected(Field{Name: "x", Type: String, Local: true, Config: "x"},
		Field{Name: "tokens", Type: SecretSlice, Local: true, EnvFallback: true})
	if got := Group(slice, slice.Inputs[1]); got != "credentials" {
		t.Errorf("a SecretSlice is in %q, want credentials", got)
	}
	// And one that is a credential and also reads like TLS stays a credential, because what a
	// person types into a masked box is the thing to keep apart.
	both := connected(Field{Name: "x", Type: String, Local: true, Config: "x"},
		Field{Name: "ssl-password", Type: Secret, Local: true, EnvFallback: true})
	if got := Group(both, both.Inputs[1]); got != "credentials" {
		t.Errorf("a Secret named ssl-password is in %q, want credentials", got)
	}
}

func TestADeclaredGroupWinsOverTheFallback(t *testing.T) {
	c := connected(
		Field{Name: "host", Type: String, Local: true, Config: "host", Group: "replica"},
		Field{Name: "user", Type: String, Local: true, Config: "user"},
		Field{Name: "bucket", Type: String, Group: "bucket"},
	)
	if got := Group(c, c.Inputs[0]); got != "replica" {
		t.Errorf("a declared group is read as %q", got)
	}
	if got := Group(c, c.Inputs[2]); got != "bucket" {
		t.Errorf("a call input's declared group is read as %q", got)
	}
}

// The fallback names the blocks of a connection and of nothing else: a call's own
// input is not headed because of what it is called.
func TestACallsOwnInputHasNoGroupUnlessItDeclaresOne(t *testing.T) {
	c := connected(serviceInputs()...)
	for _, f := range c.Inputs {
		if (f.Name == "limit" || f.Name == "table") && Group(c, f) != "" {
			t.Errorf("Group(%s) = %q, want none", f.Name, Group(c, f))
		}
	}
	// And a name that reads like a host, on a capability with no connection.
	plain := connected(Field{Name: "host", Type: String, Required: true})
	if got := Group(plain, plain.Inputs[0]); got != "" {
		t.Errorf("a call's own host is in %q", got)
	}
}
