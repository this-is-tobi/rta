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
