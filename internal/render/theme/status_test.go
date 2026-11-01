package theme

import (
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A status is valued as the word it is graded by: pkg/view takes kubectl's
// init stage off the way initStageOf does, so the value a program reads and
// the colour a person sees cannot disagree. A status with no word is graded
// as nothing.
func TestAStatusIsValuedByTheWordItIsGradedBy(t *testing.T) {
	for _, s := range []string{"Init:CrashLoopBackOff", "Init:Error", "Init:OOMKilled", "Init:0/1", "CrashLoopBackOff", "ok — 3 pods", "WARN soon · Oct 31"} {
		tbl := view.Table{Columns: []view.Column{{Name: "Status", Kind: view.KindStatus}}, Rows: [][]string{{s}}}
		word := tbl.Value(0, 0)
		if word == "" {
			if ClassifyStatus(s) != StatusNeutral {
				t.Errorf("%q has no value and is graded %v", s, ClassifyStatus(s))
			}
			continue
		}
		if ClassifyStatus(word) != ClassifyStatus(s) {
			t.Errorf("%q is valued %q, graded %v, where it is graded %v", s, word, ClassifyStatus(word), ClassifyStatus(s))
		}
	}
}

// Every status word the plugins actually emit is coloured.
//
// An unrecognised word classifies Neutral and renders with no colour at all,
// which fails in the quietest possible direction: `sys load` graded itself
// "overloaded" and `eol` graded a dead release "EOL", and both were drawn
// plainer than the "ok" beside them. Nothing said so, because a colour that is
// missing looks like a colour that was not wanted.
//
// The literals are carried here rather than derived, because deriving them
// would mean running every capability and reading its cells. To refresh:
//
//	grep -rhoE 'return "[^"]+"' builtin plugins | sort -u
//
// and add any that a KindStatus column can hold.
func TestEveryStatusWordTheBuiltinsUseIsColoured(t *testing.T) {
	cases := map[string]StatusKind{
		// sys: load, usage, temperature
		"ok":             StatusGood,
		"busy":           StatusWarn,
		"overloaded":     StatusBad,
		"WARN >80%":      StatusWarn,
		"ERROR >90%":     StatusBad,
		"WARN high":      StatusWarn,
		"ERROR critical": StatusBad,
		// cert
		"valid":   StatusGood,
		"expired": StatusBad,
		// eol
		"EOL": StatusBad,
		// net
		"open":   StatusGood,
		"closed": StatusMuted,
		"up":     StatusGood,
		"down":   StatusBad,
		// grant and the permission columns
		"read":        StatusGood,
		"write":       StatusWarn,
		"destructive": StatusBad,
		"active":      StatusGood,
		// audit's own register (report.go: stOK/stWarn/stFail/stInfo)
		"warn": StatusWarn,
		"fail": StatusBad,
		"info": StatusMuted,
		// pkg
		"outdated":       StatusWarn,
		"pending reboot": StatusWarn,
		// note: itemstore.DueStatus, which the Due column leads with
		"OVERDUE":           StatusBad,
		"OVERDUE · Sep 1":   StatusBad,
		"WARN today":        StatusWarn,
		"WARN soon · Oct 6": StatusWarn,
		"done · Oct 2":      StatusMuted,
		// profiles and the panes
		"invalid":  StatusBad,
		"disabled": StatusMuted,
		"none":     StatusMuted,
		// the lines an overview gives when its backend is not there: docker's
		// daemon, kube's cluster and its nodes, which are key/value pages
		"did not answer — Unable to connect to the server: dial tcp 127.0.0.1:1: connect: connection refused": StatusBad,
		"was never contacted — no kubeconfig":                               StatusBad,
		"answered, and refused — namespaces is forbidden":                   StatusBad,
		"answered, and did not accept this credential — Unauthorized":       StatusBad,
		"could not be read — nodes is forbidden: User cannot list resource": StatusWarn,
		// a store that is up and answers nothing until it is opened, and an
		// overview that has no reading of its backend: vault's seal state, and
		// the words docker and qdrant already say
		"sealed — 0 of 1 unseal key shares given": StatusBad,
		"unreadable — profile prod refused":       StatusWarn,
		"not initialized":                         StatusWarn,
	}
	for word, want := range cases {
		if got := ClassifyStatus(word); got != want {
			t.Errorf("ClassifyStatus(%q) = %v, want %v", word, got, want)
		}
		if ClassifyStatus(word) == StatusNeutral {
			t.Errorf("%q renders with no colour at all", word)
		}
	}
}

// The words the first-party plugins put in a status cell or at the head of an
// overview's line, read from their sources (every KindStatus column and the
// pairs of the overviews), listed by what each says about the thing it names.
//
// The guard above holds the built-ins' words; these are the plugins' that the
// host cannot derive, because a plugin is another program. The trouble words
// were drawn plainest of all (a NotReady node, a CrashLoopBackOff pod and an
// unhealthy container beside an "ok" in green), and the host is the one place
// that can colour them for every plugin at once. To refresh, read the status
// columns of rta-plugins (grep -rn KindStatus plugins) and add what a cell can
// hold.
func TestEveryStatusWordThePluginsUseIsColouredAsItsTroubleDeserves(t *testing.T) {
	cases := map[string]StatusKind{
		// kube nodes, pods, containers: the words kubectl prints in STATUS
		"Ready":                 StatusGood,
		"NotReady":              StatusBad,
		"Unknown":               StatusWarn,
		"Terminating":           StatusWarn,
		"Pending":               StatusWarn,
		"Failed":                StatusBad,
		"Evicted":               StatusBad,
		"OOMKilled":             StatusBad,
		"Error":                 StatusBad,
		"CrashLoopBackOff":      StatusBad,
		"ImagePullBackOff":      StatusBad,
		"ErrImagePull":          StatusBad,
		"Init:CrashLoopBackOff": StatusBad,
		"Init:ImagePullBackOff": StatusBad,
		"Init:Error":            StatusBad,
		"Init:OOMKilled":        StatusBad,
		"Warning":               StatusWarn,
		"expiring soon":         StatusWarn,
		"expired":               StatusBad,
		"ok":                    StatusGood,
		"unknown — not stamped at provision time": StatusWarn,
		// docker: a container's state and its healthcheck
		"unhealthy":  StatusBad,
		"restarting": StatusWarn,
		"dead":       StatusBad,
		// qdrant, etcd and redis spell theirs in the register already; redis's
		// own INFO says "err" for a failed save
		"fail — dead":        StatusBad,
		"pending — recovery": StatusWarn,
		"warn — active, but the peer cannot be messaged": StatusWarn,
		"unreachable — no route from here":               StatusBad,
		"down — link disconnected":                       StatusBad,
		"err":                                            StatusBad,
		// pkg: a tool whose version could not be compared
		"unknown — its version could not be read": StatusWarn,
		// pg, mysql and mariadb: a replication facet the role may not see
		"unknown": StatusWarn,
		// cnpg grades in ok/warn/fail/info
		"info": StatusMuted,
		// a degraded service, whoever says it
		"degraded":                   StatusWarn,
		"degraded — 2 of 3 replicas": StatusWarn,
	}
	for word, want := range cases {
		if got := ClassifyStatus(word); got != want {
			t.Errorf("ClassifyStatus(%q) = %v, want %v", word, got, want)
		}
	}
}

// These stay plain on purpose: each is a state that is fine, or one that only a
// reader who knows the workload can judge. Colouring "exited" would paint every
// one-shot job a failure, and "Running" would turn a tile of healthy pods into
// a wall of green that says nothing the ok beside it did not.
func TestTheStatesThatAreNotATroubleStayPlain(t *testing.T) {
	for _, word := range []string{
		"Running", "Succeeded", "Completed", "Bound", "Normal", "exited", "created", "paused",
		"healthy", "starting", "answering", "started", "unstarted", "learner", "leader", "follower",
		"ContainerCreating", "PodInitializing", "Init:0/1", "Init:2/3", "primary", "replica",
	} {
		if got := ClassifyStatus(word); got != StatusNeutral {
			t.Errorf("ClassifyStatus(%q) = %v, want it left plain", word, got)
		}
	}
}

// The new stems are prefixes of a whole word and of nothing else. "dead" opens
// "deadline", "err" opens "errand", and an identifier that begins with one of
// the trouble words (a pod called crashloop-demo, a queue called dead-letter)
// is a name and not a state.
func TestATroubleWordInsideAnotherWordOrANameIsNotATrouble(t *testing.T) {
	for _, in := range []string{
		"deadline", "deadline exceeded", "deadlock", "dead-letter-queue", "dead.example.com",
		"errand", "erratic", "errors-total",
		"crashloop-demo", "evicted-pods", "unhealthy_hosts", "oomkilled/last-hour",
		"unknown-host", "terminating-controller", "restarting.example.com", "degraded-mode",
		"notready-nodes", "imagepull/policy", "Init:", "Init: a stage called done",
	} {
		if got := ClassifyStatus(in); got != StatusNeutral {
			t.Errorf("ClassifyStatus(%q) = %v, want it left plain", in, got)
		}
	}
}

// The empty string stays neutral: a cell with nothing in it is not a state.
func TestNothingIsNotAState(t *testing.T) {
	for _, s := range []string{"", "   ", "3", "sha256:abc"} {
		if got := ClassifyStatus(s); got != StatusNeutral {
			t.Errorf("ClassifyStatus(%q) = %v, want neutral", s, got)
		}
	}
}

// A name is not a state. The vocabulary matches by the first letters of a value,
// and a value that names something — a cluster, a host, a bucket — began with
// them often enough that `open-webui-pg-cluster-2 — primary for 15d` was drawn
// green beside the other primaries, and a tile of `down-service` would have been
// counted a failure. A hyphenated, dotted or slashed first token is an
// identifier, and the short stems match whole words only.
func TestANameThatBeginsLikeAStatusWordIsNotOne(t *testing.T) {
	for _, name := range []string{
		"open-webui-pg-cluster-2 — primary for 15d",
		"down-service",
		"up-to-date-app",
		"upstream.example.com",
		"sealed-secrets — controller",
		"okta",
		"upstream",
		"downloads",
		"readme",
		"info-page",
		"done_deal",
		"none/of/it",
		"ok2",
	} {
		if got := ClassifyStatus(name); got != StatusNeutral {
			t.Errorf("ClassifyStatus(%q) = %v, want neutral", name, got)
		}
	}
}

// The words themselves, however a plugin continues the line after them.
func TestAStatusWordFollowedByItsDetailIsStillAStatus(t *testing.T) {
	for in, want := range map[string]StatusKind{
		"up 3 days":                 StatusGood,
		"ok — 3 pods":               StatusGood,
		"ok (12 ms)":                StatusGood,
		"open: port 443":            StatusGood,
		"ready":                     StatusGood,
		"down — connection refused": StatusBad,
		"none — the cluster has no primary right now": StatusMuted,
		"done · Oct 2":         StatusMuted,
		"warning: 3 days left": StatusWarn,
		"failed: exit 1":       StatusBad,
	} {
		if got := ClassifyStatus(in); got != want {
			t.Errorf("ClassifyStatus(%q) = %v, want %v", in, got, want)
		}
	}
}
