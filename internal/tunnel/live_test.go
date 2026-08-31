//go:build livecluster

// The questions a stub cannot answer, run against a real cluster.
//
//	kind create cluster --name rta-lab
//	kubectl apply -f internal/tunnel/testdata/kube-lab.yaml
//	RTA_TEST_KUBE=kind-rta-lab/databases/svc/postgres:5432 \
//	RTA_TEST_SECRET=postgres-creds \
//	  go test ./internal/tunnel/ -tags livecluster -count=1 -v
//
// RTA_TEST_KUBE_DENIED names a second context whose identity may port-forward
// and may not read secrets, which is what makes the "different permission"
// hint a claim about the cluster rather than about the message.
package tunnel

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"testing"
	"time"
)

// The two questions the design left open, which needed a cluster — setup is
// the package doc above. Scoped to just this test:
//
//	go test ./internal/tunnel/ -tags livecluster -count=1 -run TestTunnelAgainstARealCluster -v

func TestTunnelAgainstARealCluster(t *testing.T) {
	spec := os.Getenv("RTA_TEST_KUBE")
	if spec == "" {
		t.Skip("set RTA_TEST_KUBE=context/namespace/kind/name:port")
	}

	// Question 1 — real setup cost. The stubbed figure was 52 ms and excluded
	// the cluster round trip entirely, which is the part an operator waits
	// for. Measured per open, several times, because the first one pays for
	// whatever kubectl caches.
	const runs = 5
	var opens, closes []time.Duration
	for i := 0; i < runs; i++ {
		t0 := time.Now()
		tun, verr := Open(context.Background(), "live", Target{Kube: spec})
		if verr != nil {
			t.Fatalf("open %d: %v", i, verr)
		}
		opens = append(opens, time.Since(t0))

		if i == 0 {
			// Question 3 — whether "Forwarding from" means traffic flows.
			// Parsing the line proves kubectl printed it. Speaking the
			// protocol proves there is a server at the other end.
			assertPostgresAnswers(t, tun.Endpoint)
		}

		t1 := time.Now()
		tun.Close()
		closes = append(closes, time.Since(t1))
		if tun.TimedOut() {
			t.Error("close fell through to its timeout")
		}
		// A closed tunnel must stop accepting. This is the assertion that
		// makes teardown a fact rather than a call that returned.
		if c, err := net.DialTimeout("tcp",
			fmt.Sprintf("%s:%d", tun.Host, tun.Port), 500*time.Millisecond); err == nil {
			c.Close()
			t.Error("the forward still accepts connections after Close")
		}
	}
	t.Logf("open  min %s median %s max %s", min(opens), median(opens), max(opens))
	t.Logf("close min %s median %s max %s", min(closes), median(closes), max(closes))
}

// assertPostgresAnswers speaks enough of the protocol to prove a server is
// there: an SSLRequest is eight bytes and every PostgreSQL answers it with a
// single byte, 'S' or 'N', before any authentication happens.
func assertPostgresAnswers(t *testing.T, ep Endpoint) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ep.Host, ep.Port), 5*time.Second)
	if err != nil {
		t.Fatalf("kubectl said it was forwarding and nothing accepted a connection: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	req := make([]byte, 8)
	binary.BigEndian.PutUint32(req[0:], 8)
	binary.BigEndian.PutUint32(req[4:], 80877103) // SSLRequest
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write: %v", err)
	}
	reply := make([]byte, 1)
	if _, err := conn.Read(reply); err != nil {
		t.Fatalf("the connection was accepted and the server never answered: %v", err)
	}
	if reply[0] != 'S' && reply[0] != 'N' {
		t.Fatalf("answered %q, which is not a PostgreSQL SSL reply", reply[0])
	}
	t.Logf("traffic flows: PostgreSQL answered the SSL request with %q", reply[0])
}

func min(ds []time.Duration) time.Duration {
	m := ds[0]
	for _, d := range ds {
		if d < m {
			m = d
		}
	}
	return m
}

func max(ds []time.Duration) time.Duration {
	m := ds[0]
	for _, d := range ds {
		if d > m {
			m = d
		}
	}
	return m
}

func median(ds []time.Duration) time.Duration {
	s := append([]time.Duration(nil), ds...)
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return s[len(s)/2]
}

// Run against a real cluster:
//
//	go test ./internal/tunnel/ -tags livecluster -count=1 \
//	  -run TestSecretsAgainstARealCluster
//
// RTA_TEST_KUBE names the target, in the same grammar an operator writes.
func TestSecretsAgainstARealCluster(t *testing.T) {
	spec := os.Getenv("RTA_TEST_KUBE")
	if spec == "" {
		t.Skip("set RTA_TEST_KUBE=context/namespace/kind/name:port")
	}
	got, verr := Secrets(context.Background(), "live", Target{
		Kube:   spec,
		Secret: os.Getenv("RTA_TEST_SECRET"),
		From:   map[string]string{"user": "username", "password": "password", "database": "database"},
	})
	if verr != nil {
		t.Fatalf("Secrets: %v", verr)
	}
	for _, input := range []string{"user", "password", "database"} {
		if got[input] == "" {
			t.Errorf("%s came back empty", input)
		}
	}
	// Length, never the value: a test log is a place credentials go to live
	// forever.
	t.Logf("filled user(%d) password(%d) database(%d)",
		len(got["user"]), len(got["password"]), len(got["database"]))
}

// Each failure an operator can actually reach, against a real cluster.
func TestSecretFailuresAgainstARealCluster(t *testing.T) {
	base := os.Getenv("RTA_TEST_KUBE")
	if base == "" {
		t.Skip("set RTA_TEST_KUBE")
	}
	denied := os.Getenv("RTA_TEST_KUBE_DENIED")

	cases := []struct {
		name, kube, secret string
		from               map[string]string
		wantCode           string
	}{
		{"no such secret", base, "nope-does-not-exist",
			map[string]string{"password": "password"}, "tunnel.secret.missing"},
		{"no such key", base, os.Getenv("RTA_TEST_SECRET"),
			map[string]string{"password": "nope"}, "tunnel.secret.key.missing"},
		{"not allowed to read it", denied, os.Getenv("RTA_TEST_SECRET"),
			map[string]string{"password": "password"}, "tunnel.secret.denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.kube == "" {
				t.Skip("no context for this case")
			}
			_, verr := Secrets(context.Background(), "live",
				Target{Kube: tc.kube, Secret: tc.secret, From: tc.from})
			if verr == nil {
				t.Fatal("no error")
			}
			if verr.Code != tc.wantCode {
				t.Errorf("code = %s, want %s", verr.Code, tc.wantCode)
			}
			t.Logf("%s\n  %s\n  hint: %s", verr.Code, verr.Message, verr.Hint)
		})
	}
}
