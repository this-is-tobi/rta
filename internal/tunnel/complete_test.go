package tunnel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// clusterFake answers every listing CompleteKube and CompleteSecretRef make,
// and records each invocation's argv — the assertions here are as much about
// what was asked as about what came back, because "pinned to the typed
// context" is an argv property.
func clusterFake(t *testing.T) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "argv.log")
	fakeKubectl(t, `printf '%s ' "$@" >> `+log+`; printf '\n' >> `+log+`
case "$*" in
  *"config get-contexts"*) printf 'kind-kind\nhomelab\narn:aws:eks:eu-west-1:1:cluster/prod\n' ;;
  *"get namespaces"*) printf 'namespace/monitoring\nnamespace/databases\n' ;;
  *"get secret pg-creds"*) printf 'password\nusername\n' ;;
  *"get secrets"*) printf 'secret/pg-creds\nsecret/api-token\n' ;;
  *"get svc postgres"*) printf '5432 9187' ;;
  *"get svc"*) printf 'service/redis\nservice/postgres\n' ;;
  *"get pod postgres-0"*) printf '5432' ;;
  *"get pod empty-0"*) printf '' ;;
  *"get deploy app"*) printf '8080' ;;
esac
`)
	return log
}

func asked(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return ""
	}
	return string(b)
}

// Each segment completes from a listing pinned to the segments already typed
// — `--context` and `--namespace` explicit on every call, never the ambient
// current-context. Typing `prod-mirror` must not poke whatever `prod`
// happens to point at, and the argv is where that property lives.
func TestCompleteKubeWalksTheGrammarSegmentBySegment(t *testing.T) {
	for _, tc := range []struct {
		partial string
		argv    string   // "" for a segment that must spawn nothing
		items   []string // full field values, ready to accept
	}{
		{"", "config get-contexts -o name",
			[]string{"homelab/", "kind-kind/"}},
		{"homelab/", "--context homelab get namespaces -o name",
			[]string{"homelab/databases/", "homelab/monitoring/"}},
		{"homelab/da", "--context homelab get namespaces -o name",
			[]string{"homelab/databases/", "homelab/monitoring/"}},
		{"homelab/databases/", "",
			[]string{"homelab/databases/svc/", "homelab/databases/pod/",
				"homelab/databases/deploy/", "homelab/databases/sts/"}},
		{"homelab/databases/svc/", "--context homelab --namespace databases get svc -o name",
			[]string{"homelab/databases/svc/postgres:", "homelab/databases/svc/redis:"}},
		{"homelab/databases/svc/postgres:", "--context homelab --namespace databases get svc postgres -o jsonpath={.spec.ports[*].port}",
			[]string{"homelab/databases/svc/postgres:5432", "homelab/databases/svc/postgres:9187"}},
	} {
		t.Run("«"+tc.partial+"»", func(t *testing.T) {
			log := clusterFake(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, verr := CompleteKube(ctx, tc.partial)
			if verr != nil {
				t.Fatalf("completing %q: %s", tc.partial, verr.Message)
			}
			if tc.argv == "" {
				if got := asked(t, log); got != "" {
					t.Fatalf("a local segment ran kubectl: %s", got)
				}
			} else if !strings.Contains(asked(t, log), tc.argv) {
				t.Errorf("asked:\n%swant a call containing:\n%s", asked(t, log), tc.argv)
			}
			if strings.Join(c.Items, " ") != strings.Join(tc.items, " ") {
				t.Errorf("items = %v, want %v", c.Items, tc.items)
			}
		})
	}
}

// The kind segment is a static list, so it must complete on a machine with no
// kubectl at all — the branch returns before kubectl is even looked for.
func TestTheKindSegmentNeedsNoKubectl(t *testing.T) {
	saved := kubectl
	kubectl = filepath.Join(t.TempDir(), "nothing-here")
	t.Cleanup(func() { kubectl = saved })

	c, verr := CompleteKube(context.Background(), "homelab/databases/")
	if verr != nil {
		t.Fatalf("the static segment failed without kubectl: %s", verr.Message)
	}
	if len(c.Items) != len(kubeKinds) {
		t.Errorf("items = %v, want the four kinds", c.Items)
	}
}

// A context the grammar cannot hold is not offered. parseKube splits on
// slashes, so a context named like an EKS ARN would complete to a value the
// very next validation refuses — a suggestion list must not hand out traps.
func TestAContextTheGrammarCannotHoldIsNotOffered(t *testing.T) {
	clusterFake(t)
	c, verr := CompleteKube(context.Background(), "")
	if verr != nil {
		t.Fatal(verr.Message)
	}
	for _, item := range c.Items {
		if strings.Contains(item, "arn:aws") {
			t.Errorf("an unusable context was offered: %v", c.Items)
		}
	}
	if len(c.Items) != 2 {
		t.Errorf("items = %v, want the two usable contexts", c.Items)
	}
}

// Ports are read from the spec path the kind keeps them under — a pod's
// containers, a workload's template — because completing the port is the one
// segment where the answer is knowledge, not spelling.
func TestPortsAreReadFromTheSpecPathTheKindKeeps(t *testing.T) {
	for partial, path := range map[string]string{
		"homelab/databases/pod/postgres-0:": "get pod postgres-0 -o jsonpath={.spec.containers[*].ports[*].containerPort}",
		"homelab/databases/deploy/app:":     "get deploy app -o jsonpath={.spec.template.spec.containers[*].ports[*].containerPort}",
	} {
		log := clusterFake(t)
		if _, verr := CompleteKube(context.Background(), partial); verr != nil {
			t.Fatalf("%q: %s", partial, verr.Message)
		}
		if !strings.Contains(asked(t, log), path) {
			t.Errorf("completing %q asked:\n%swant %s", partial, asked(t, log), path)
		}
	}
}

// An object that declares no ports completes to nothing, without an error:
// the operator types the number, which is exactly what fail-open means here.
func TestAnObjectWithNoDeclaredPortsCompletesToNothing(t *testing.T) {
	clusterFake(t)
	c, verr := CompleteKube(context.Background(), "homelab/databases/pod/empty-0:")
	if verr != nil {
		t.Fatalf("an empty listing is not a failure: %s", verr.Message)
	}
	if len(c.Items) != 0 {
		t.Errorf("items = %v, want none", c.Items)
	}
}

// **Key completion never asks for values.** There is no keys-only read in the
// API — `get` on secrets grants the values — so the one control rta owns is
// the output format: a go-template that has kubectl print key names alone,
// leaving the values in kubectl's process, not this one. A
// regression to `-o json` here would pull every value of the Secret into
// rta's memory for a suggestion list.
func TestSecretKeyCompletionNeverAsksForValues(t *testing.T) {
	log := clusterFake(t)
	coord := "homelab/databases/svc/postgres:5432"
	c, verr := CompleteSecretRef(context.Background(), coord, "pg-creds/")
	if verr != nil {
		t.Fatal(verr.Message)
	}
	argv := asked(t, log)
	if !strings.Contains(argv, `go-template={{range $k, $v := .data}}{{$k}}{{"\n"}}{{end}}`) {
		t.Errorf("the key listing did not use the keys-only template:\n%s", argv)
	}
	if strings.Contains(argv, "-o json") {
		t.Errorf("the key listing asked for the whole Secret, values included:\n%s", argv)
	}
	want := []string{"pg-creds/password", "pg-creds/username"}
	if strings.Join(c.Items, " ") != strings.Join(want, " ") {
		t.Errorf("items = %v, want %v", c.Items, want)
	}
}

// The Secret half lists names in the coordinate's own namespace — the same
// boundary the call-time read enforces, held at completion time too.
func TestSecretNamesComeFromTheCoordinatesNamespace(t *testing.T) {
	log := clusterFake(t)
	c, verr := CompleteSecretRef(context.Background(), "homelab/databases/svc/postgres:5432", "")
	if verr != nil {
		t.Fatal(verr.Message)
	}
	if !strings.Contains(asked(t, log), "--context homelab --namespace databases get secrets -o name") {
		t.Errorf("asked:\n%s", asked(t, log))
	}
	want := []string{"api-token/", "pg-creds/"}
	if strings.Join(c.Items, " ") != strings.Join(want, " ") {
		t.Errorf("items = %v, want %v", c.Items, want)
	}
}

// Accepting every suggestion in turn spells a coordinate CheckKube accepts —
// each item carries the separator the grammar wants next, so the completion
// key composes into a full, valid value rather than stranding the cursor.
func TestCompletedCoordinatesSatisfyCheckKube(t *testing.T) {
	clusterFake(t)
	partial := ""
	for range [5]int{} {
		c, verr := CompleteKube(context.Background(), partial)
		if verr != nil {
			t.Fatalf("completing %q: %s", partial, verr.Message)
		}
		if len(c.Items) == 0 {
			t.Fatalf("completing %q offered nothing", partial)
		}
		partial = c.Items[0]
	}
	if verr := CheckKube(partial); verr != nil {
		t.Fatalf("five accepts spelled %q, which is not a coordinate: %s", partial, verr.Message)
	}
}

// A refused listing fails open in kubectl's own words: a one-line refusal
// beside a field that still takes typing, never a gate. The hint says which
// verb was missing, because `list` is not a permission the forward needs and
// an operator scoped to exactly the forward is the expected case, not the
// broken one.
func TestARefusedListingFailsOpenInKubectlsWords(t *testing.T) {
	fakeKubectl(t, "echo 'Error from server (Forbidden): namespaces is forbidden: User \"dev\" cannot list resource \"namespaces\"' >&2; exit 1\n")
	_, verr := CompleteKube(context.Background(), "homelab/")
	if verr == nil {
		t.Fatal("a forbidden listing reported success")
	}
	if verr.Code != "tunnel.list.denied" {
		t.Errorf("code = %s, want tunnel.list.denied", verr.Code)
	}
	if !strings.Contains(verr.Message, "namespaces in homelab") {
		t.Errorf("the refusal does not say what was being listed: %s", verr.Message)
	}
}

// A listing that hangs is bounded by the caller's context and classified as a
// timeout, not as whatever half-written stderr the kill left behind.
func TestAHangingListingIsBoundedByTheCallersContext(t *testing.T) {
	fakeKubectl(t, "exec sleep 5\n")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, verr := CompleteKube(ctx, "homelab/")
	if verr == nil {
		t.Fatal("a hung listing reported success")
	}
	if verr.Code != "tunnel.list.timeout" {
		t.Errorf("code = %s, want tunnel.list.timeout", verr.Code)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("the caller waited %s on a 100ms deadline", took)
	}
}

// Partials the grammar cannot complete are refused with the grammar, not
// guessed at.
func TestUncompletablePartialsAreRefused(t *testing.T) {
	clusterFake(t)
	if _, verr := CompleteKube(context.Background(), "a/b/c/d/e"); verr == nil {
		t.Error("five segments completed")
	}
	coord := "homelab/databases/svc/postgres:5432"
	if _, verr := CompleteSecretRef(context.Background(), coord, "a/b/c"); verr == nil {
		t.Error("a two-slash secret reference completed")
	}
	if _, verr := CompleteSecretRef(context.Background(), "not-a-coordinate", ""); verr == nil {
		t.Error("a connection with a broken coordinate listed secrets anyway")
	}
}

// Without kubectl, completion says so once and leaves the field alone — the
// same fail-open shape as a refusal, with the same hint discipline.
func TestAMissingKubectlIsAnAssistLostNotAGate(t *testing.T) {
	saved := kubectl
	kubectl = filepath.Join(t.TempDir(), "nothing-here")
	t.Cleanup(func() { kubectl = saved })
	_, verr := CompleteKube(context.Background(), "homelab/")
	if verr == nil || verr.Code != "tunnel.kubectl.missing" {
		t.Fatalf("verr = %+v, want tunnel.kubectl.missing", verr)
	}
}
