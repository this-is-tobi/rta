package profile

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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

// The environment channel is a profile's, and the name an env token is made
// from is not injective: "ad hoc" and a profile called ad-hoc spell the same
// variable. A call that read it would send what was exported for that profile
// to whatever the call was aimed at, and Fill ranks a variable above a
// reference, so it would also replace the credential the call asked for.
func TestAnAdHocConnectionReadsNoVariableAProfileOwns(t *testing.T) {
	owned := plugin.ProfileEnvVar("ad-hoc", "password")
	if plugin.ProfileEnvVar(AdHocName, "password") != owned {
		t.Fatalf("the premise is gone: %q and ad-hoc no longer spell one variable", AdHocName)
	}
	look := func(key string) (string, bool) {
		if key == owned {
			return "the-other-profiles-password", true
		}
		return "", false
	}
	if got := Bind(AdHocName, config.Connection{}, pgCap(), look); len(got) != 0 {
		t.Errorf("an ad hoc connection read from the environment: %v", got)
	}
	if got := Bind("ad-hoc", config.Connection{}, pgCap(), look); got["password"] != "the-other-profiles-password" {
		t.Errorf("the profile called ad-hoc lost its own variable: %v", got)
	}

	conn := config.Connection{Secrets: map[string]string{"password": "kv:entry"}}
	read := func(ref string) (string, *view.Error) { return "from-the-store", nil }
	got, verr := Fill(context.Background(), AdHocName, conn, pgCap(), nil, look, read)
	if verr != nil {
		t.Fatalf("Fill: %v", verr)
	}
	if got["password"] != "from-the-store" {
		t.Errorf("the reference the call stated was replaced by %v", got["password"])
	}
}

// The words that say where a call stated for itself went are one sentence for
// the line above a CLI result and the TUI's picker, said from the three facts a
// call can state, and in the order that says the most specific first.
func TestAdHocWordsSayWhereTheCallWentFromWhatItStated(t *testing.T) {
	for name, tc := range map[string]struct {
		kube, from string
		secrets    int
		want       string
	}{
		"a forward":                      {"homelab/databases/svc/postgres:5432", "", 0, "through homelab/databases/svc/postgres:5432"},
		"a forward and its credential":   {"homelab/databases/svc/postgres:5432", "", 1, "through homelab/databases/svc/postgres:5432"},
		"only the cluster of the Secret": {"", "homelab/databases", 1, "credentials from homelab/databases"},
		"only credentials by reference":  {"", "", 2, "credentials by reference"},
		"nothing":                        {"", "", 0, ""},
	} {
		if got := AdHocWords(tc.kube, tc.from, tc.secrets); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}
