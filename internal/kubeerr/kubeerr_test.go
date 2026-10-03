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
