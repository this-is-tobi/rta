package theme

import "testing"

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
