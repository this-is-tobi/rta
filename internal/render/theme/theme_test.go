package theme

import "testing"

func TestClassifyStatus(t *testing.T) {
	tests := []struct {
		in   string
		want StatusKind
	}{
		{"ok", StatusGood},
		{"OK", StatusGood},
		{"valid", StatusGood},
		{"open", StatusGood},
		{"read", StatusGood},
		{"WARN <30d", StatusWarn},
		{"write", StatusWarn},
		{"ERROR: connection refused", StatusBad},
		{"EXPIRED", StatusBad},
		{"INVALID: x509", StatusBad},
		{"destructive", StatusBad},
		{"closed", StatusMuted},
		{"info", StatusMuted},
		{"", StatusNeutral},
		{"something-else", StatusNeutral},
	}
	for _, tt := range tests {
		if got := ClassifyStatus(tt.in); got != tt.want {
			t.Errorf("ClassifyStatus(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// The text colours read on a white terminal and on a black one. Good, warn and
// bad were chosen for black alone: warn was 13:1 there and 1.6:1 on white, so
// on a light terminal "ok" and "warn" were near-white text on white, and no
// palette detection can be relied on to say which terminal this is. Three is
// the floor the label colour's own comment holds Primary to.
func TestTheTextColoursAreReadableOnLightAndDarkTerminals(t *testing.T) {
	for name, hex := range map[string]string{
		"primary": primaryHex, "accent": accentHex, "muted": mutedHex,
		"label": labelHex, "good": goodHex, "warn": warnHex, "bad": badHex,
	} {
		l := relativeLuminance(hex)
		if onWhite := 1.05 / (l + 0.05); onWhite < 3 {
			t.Errorf("%s %s is %.2f:1 on white, under 3:1", name, hex, onWhite)
		}
		if onBlack := (l + 0.05) / 0.05; onBlack < 3 {
			t.Errorf("%s %s is %.2f:1 on black, under 3:1", name, hex, onBlack)
		}
	}
}
