package profile

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// A connection typed on a command line is held to what the same connection
// stated in a profile is held to, by the same checks. These are the refusals a
// profile's Lookup makes that a command line has to make too.

func TestAnAdHocConnectionIsHeldToWhatAStoredOneIs(t *testing.T) {
	reg := tunnelledRegistry(t)
	c := tunnelCap()
	for name, tc := range map[string]struct {
		conn config.Connection
		want string // a code, or "" for accepted
	}{
		"a forward":                        {config.Connection{Kube: "homelab/databases/svc/postgres:5432"}, ""},
		"a forward and a credential":       {config.Connection{Kube: "homelab/databases/svc/postgres:5432", Secrets: map[string]string{"password": "kube:pg-creds/password"}}, ""},
		"a coordinate that does not parse": {config.Connection{Kube: "postgres"}, "core.adhoc.tunnel"},
		"two forwards":                     {config.Connection{Kube: "homelab/databases/svc/postgres:5432", SSH: "bastion/db:5432"}, "core.adhoc.tunnel"},
		"a forward and a second cluster":   {config.Connection{Kube: "homelab/databases/svc/postgres:5432", SecretsFrom: "homelab/databases"}, "core.adhoc.tunnel"},
		"a secrets-from that is not one":   {config.Connection{SecretsFrom: "homelab"}, "core.adhoc.tunnel"},
		"a reference with no source":       {config.Connection{Secrets: map[string]string{"password": "hunter2"}}, "core.adhoc.secret.scheme"},
		"a cluster reference, no cluster":  {config.Connection{Secrets: map[string]string{"password": "kube:pg-creds/password"}}, "core.adhoc.secrets"},
		"a credential onto no input":       {config.Connection{Secrets: map[string]string{"nothing": "kv:entry"}, SecretsFrom: "homelab/databases"}, "core.adhoc.secrets"},
	} {
		verr := AdHoc(c, tc.conn, reg)
		switch {
		case tc.want == "" && verr != nil:
			t.Errorf("%s: refused: %s", name, verr.Message)
		case tc.want != "" && (verr == nil || verr.Code != tc.want):
			t.Errorf("%s: got %v, want %s", name, verr, tc.want)
		}
	}
}

// The name is not one a profile can have, so a hint that carries it does not
// reach a profile a person later called that.
func TestTheAdHocNameIsNotAProfileReference(t *testing.T) {
	if config.ValidRef(AdHocName) || config.ValidName(AdHocName) {
		t.Errorf("%q is a profile name or reference", AdHocName)
	}
	if !strings.Contains(AdHocName, " ") {
		t.Errorf("%q has no space to keep it from being one", AdHocName)
	}
}
