package pluginhost

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// The claim, checked where it can be. PR_SET_DUMPABLE=0 is asserted on
// the host in two separate places while nothing set it — a documented control
// that does not exist is worse than an absent one, because somebody builds a
// threat model on the document.
//
// On Linux this reads the kernel's own answer rather than trusting the return
// value of the prctl. Elsewhere it asserts the honest no-op: HardenSelf must
// be safe to call, because main calls it unconditionally.
func TestHardenSelfMatchesWhatTheADRClaims(t *testing.T) {
	HardenSelf()
	HardenSelf() // idempotent: main calls it once, tests call it repeatedly

	if runtime.GOOS != "linux" {
		// The platforms that do nothing must say so somewhere a reader will
		// find it; self_other.go carries the per-platform reasons.
		return
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Skipf("no /proc/self/status: %v", err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		if !strings.HasPrefix(line, "Dumpable:") {
			continue
		}
		if got := strings.TrimSpace(strings.TrimPrefix(line, "Dumpable:")); got != "0" {
			t.Errorf("Dumpable = %q, want 0 — /proc/<pid>/environ and mem are readable "+
				"by any process at this uid, which is where the age identity and the "+
				"grant seal key live", got)
		}
		return
	}
	t.Skip("no Dumpable line in /proc/self/status")
}
