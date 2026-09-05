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
		// profiles and the panes
		"invalid":  StatusBad,
		"disabled": StatusMuted,
		"none":     StatusMuted,
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
