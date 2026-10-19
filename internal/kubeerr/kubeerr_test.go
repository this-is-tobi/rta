package kubeerr

import "testing"

func TestUnreachableAndServerReadBothSpellingsOfADeadCluster(t *testing.T) {
	for _, c := range []struct {
		name, stderr, why, server string
	}{
		{"the current klog line",
			`E1003 14:46:33.515018 94197 memcache.go:265] "Unhandled Error" err="couldn't get current server API group list: ` +
				`Get \"https://127.0.0.1:1/api?timeout=32s\": dial tcp 127.0.0.1:1: connect: connection refused"`,
			"connection refused", "https://127.0.0.1:1"},
		{"the old sentence",
			`Unable to connect to the server: dial tcp 10.0.0.9:6443: i/o timeout`,
			"i/o timeout", "10.0.0.9:6443"},
		{"a name that does not resolve",
			`Unable to connect to the server: dial tcp: lookup api.example.test: no such host`,
			"no such host", ""},
		{"a refusal that is not about the network", `Error from server (Forbidden): pods is forbidden`, "", ""},
		{"a credential plugin that failed before any dial",
			`Unable to connect to the server: getting credentials: exec: fork/exec /opt/acme/acme-auth: no such file`, "", ""},
		{"nothing", "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Unreachable(c.stderr); got != c.why {
				t.Errorf("Unreachable = %q, want %q", got, c.why)
			}
			if c.why != "" {
				if got := Server(c.stderr); got != c.server {
					t.Errorf("Server = %q, want %q", got, c.server)
				}
			}
		})
	}
}

// What a kubectl failure says about the account is three different things with
// three different fixes, and a dashboard tile stops asking for only one of
// them. The API server answering 401 is a login it did not accept; a credential
// plugin that failed, or that this machine lacks, never reached the server at
// all, so there was nothing to refuse and the tile has to keep asking until the
// person has logged in again; and a 403 is neither (the account is known).
func TestAKubectlFailureIsALoginNotAcceptedOrACredentialThatIsNotThere(t *testing.T) {
	for _, c := range []struct {
		name, stderr                   string
		notAuthenticated, helperFailed bool
		missing                        string
	}{
		{"an expired login", `error: You must be logged in to the server (Unauthorized)`, true, false, ""},
		{"a 401 from the api server", `Error from server (Unauthorized): unknown`, true, false, ""},
		{"a request without a credential", `error: You must be logged in to the server (the server has asked for the client to provide credentials)`,
			true, false, ""},
		{"the same, from a connection", `Unable to connect to the server: the server has asked for the client to provide credentials`,
			true, false, ""},
		{"a credential plugin that failed", `Unable to connect to the server: getting credentials: exec: executable tsh failed with exit code 1`,
			false, true, ""},
		{"a credential plugin kubectl cannot use", `error: exec plugin: invalid apiVersion "client.authentication.k8s.io/v1alpha1"`,
			false, true, ""},
		{"an auth provider kubectl no longer ships", `error: no Auth Provider found for name "gcp"`, false, true, ""},
		{"a credential plugin that is not installed", `Unable to connect to the server: getting credentials: exec: executable tsh not found`,
			false, true, "tsh"},
		{"a credential plugin named by its path", `Unable to connect to the server: getting credentials: exec: fork/exec /opt/acme/acme-auth: no such file or directory`,
			false, true, "acme-auth"},
		{"an account the cluster knows and refused", `Error from server (Forbidden): pods is forbidden: User "u" cannot list resource "pods"`,
			false, false, ""},
		{"a cluster that did not answer", `Unable to connect to the server: dial tcp 10.0.0.9:6443: i/o timeout`, false, false, ""},
		{"nothing", "", false, false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := NotAuthenticated(c.stderr); got != c.notAuthenticated {
				t.Errorf("NotAuthenticated = %v, want %v", got, c.notAuthenticated)
			}
			if got := CredentialHelperFailed(c.stderr); got != c.helperFailed {
				t.Errorf("CredentialHelperFailed = %v, want %v", got, c.helperFailed)
			}
			exe, ok := CredentialPluginMissing(c.stderr)
			if ok != (c.missing != "") || exe != c.missing {
				t.Errorf("CredentialPluginMissing = %q, %v, want %q", exe, ok, c.missing)
			}
		})
	}
}
