package plugin

import (
	"testing"
	"time"
)

func remoteCapability() Capability {
	return Capability{ID: "pg.status", Safety: Read,
		Inputs: []Field{{Name: "host", Type: String, Config: "host"}}}
}

func TestACapabilityThatReachesAConnectionHasTheRemotePace(t *testing.T) {
	if got := remoteCapability().Pace(); got != PaceRemote {
		t.Errorf("Pace = %v, want %v", got, PaceRemote)
	}
	if got := (Capability{ID: "sys.load", Safety: Read}).Pace(); got != PaceLocal {
		t.Errorf("a capability that reads the machine has Pace %v, want %v", got, PaceLocal)
	}
}

func TestADeclaredPaceBeatsTheKindOfWork(t *testing.T) {
	c := remoteCapability()
	c.Refresh = time.Minute
	if got := c.Pace(); got != time.Minute {
		t.Errorf("Pace = %v, want the declared minute", got)
	}
}

// A configuration lengthens any pace and shortens only the one nobody declared.
func TestAConfiguredPaceCannotGoUnderADeclaredOne(t *testing.T) {
	declared := Capability{ID: "eol.watch", Safety: Read, Refresh: 2 * time.Hour}
	for _, tc := range []struct {
		name string
		c    Capability
		want time.Duration
		got  time.Duration
		cut  bool
	}{
		{"nothing stated", declared, 0, 2 * time.Hour, false},
		{"slower than declared", declared, 3 * time.Hour, 3 * time.Hour, false},
		{"faster than declared", declared, time.Minute, 2 * time.Hour, true},
		{"a default the capability did not declare, shortened", remoteCapability(), 10 * time.Second, 10 * time.Second, false},
		{"a default, lengthened", remoteCapability(), 5 * time.Minute, 5 * time.Minute, false},
		{"under the timer's tick", remoteCapability(), time.Second, PaceLocal, true},
		{"nothing stated of a default", remoteCapability(), 0, PaceRemote, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, cut := ClampPace(tc.c, tc.want)
			if got != tc.got || cut != tc.cut {
				t.Errorf("ClampPace(%v) = %v, %v; want %v, %v", tc.want, got, cut, tc.got, tc.cut)
			}
		})
	}
}
