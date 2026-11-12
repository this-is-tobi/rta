package pkg

import (
	"context"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The OS's own state: pending system updates, whether a reboot is owed, and
// the kernel that is running against the newest one installed — the three
// facts a package table does not carry and an outage post-mortem asks for.

func osCapability() plugin.Capability {
	return host(plugin.Capability{
		ID:         "pkg.os",
		Summary:    "The OS's own pending updates, whether a reboot is owed, and the kernel gap",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "On macOS, what `softwareupdate --list` offers and which of it restarts " +
			"the machine once installed; macOS keeps no flag for a reboot owed, so there " +
			"is no reboot line there. On Linux, whether the system says a reboot is required " +
			"(/var/run/reboot-required on Debian, needs-restarting on RHEL) and the " +
			"running kernel against the newest installed one — a machine that was " +
			"upgraded and never rebooted runs the old kernel with the new one on disk, " +
			"and nothing but this row says so.\n\n" +
			"Nothing here installs or reboots. Installing OS updates needs root and is " +
			"`sudo softwareupdate --install --all` or the OS manager's own upgrade; " +
			"rebooting is yours.",
		Run: runOS,
	})
}

// osState is what pkg.os and the overview share.
type osState struct {
	Updates []osUpdate
	// RebootKnown says the reader can tell whether a reboot is owed, which
	// Linux writes down and macOS does not. RebootRequired means one is owed
	// now, for something already installed.
	//
	// macOS's reading once set RebootRequired for an update softwareupdate
	// only offered, whose Action is restart: the overview then said "warn —
	// reboot owed" ahead of everything behind, on a machine where a reboot
	// would have installed nothing and changed nothing. What restarts is a
	// fact about the offered update, said on its row and in the count of
	// them; with no flag to read, macOS has no reboot line at all, since
	// "ok" there would be a claim nobody looked to make.
	RebootKnown    bool
	RebootRequired bool
	RebootReason   string
	KernelRunning  string
	KernelNewest   string
	Notes          []string
}

type osUpdate struct {
	Label, Title, Version string
	Restart               bool
}

func (s osState) kernelBehind() bool {
	return s.KernelRunning != "" && s.KernelNewest != "" && s.KernelRunning != s.KernelNewest &&
		semverLess(s.KernelRunning, s.KernelNewest)
}

// osReader is how the overview and pkg.os read the OS, a seam for the tests so
// that the rows are the same on macOS and on Linux.
var osReader = readOS

func readOS(ctx context.Context) osState {
	if runtime.GOOS == "darwin" {
		return readMacOS(ctx)
	}
	return readLinux(ctx)
}

// readMacOS parses `softwareupdate --list`:
//
//   - Label: macOS Sonoma 14.6.1-23G93
//     Title: macOS Sonoma 14.6.1, Version: 14.6.1, Size: 1234KiB, Recommended: YES, Action: restart,
func readMacOS(ctx context.Context) osState {
	var st osState
	out, verr := run(ctx, "softwareupdate", "--list")
	if verr != nil {
		st.Notes = append(st.Notes, verr.Message)
		return st
	}
	var cur *osUpdate
	for _, line := range lines(out) {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "* Label:"):
			st.Updates = append(st.Updates, osUpdate{Label: strings.TrimSpace(strings.TrimPrefix(t, "* Label:"))})
			cur = &st.Updates[len(st.Updates)-1]
		case cur != nil && strings.HasPrefix(t, "Title:"):
			for _, part := range strings.Split(t, ",") {
				k, v, ok := strings.Cut(strings.TrimSpace(part), ":")
				if !ok {
					continue
				}
				v = strings.TrimSpace(v)
				switch strings.TrimSpace(k) {
				case "Title":
					cur.Title = v
				case "Version":
					cur.Version = v
				case "Action":
					cur.Restart = v == "restart"
				}
			}
		}
	}
	return st
}

// readLinux reads the reboot flag the distribution family writes, and the
// kernel gap from the package database.
func readLinux(ctx context.Context) osState {
	st := osState{RebootKnown: true}
	if _, err := os.Stat("/var/run/reboot-required"); err == nil {
		st.RebootRequired, st.RebootReason = true, "/var/run/reboot-required is present"
	} else if _, err := lookPath("needs-restarting"); err == nil {
		// Exit 1 means a reboot is needed; 0 means not.
		if answer, verr := runStatus(ctx, "needs-restarting", "-r"); verr == nil && answer.code == 1 {
			st.RebootRequired, st.RebootReason = true, "needs-restarting says so"
		}
	}
	if out, verr := run(ctx, "uname", "-r"); verr == nil {
		st.KernelRunning = strings.TrimSpace(out)
	}
	st.KernelNewest = newestInstalledKernel(ctx)
	if st.kernelBehind() && !st.RebootRequired {
		st.RebootRequired, st.RebootReason = true, "a newer kernel is installed than the one running"
	}
	return st
}

// newestInstalledKernel asks whichever package database is here for the
// kernel packages and returns the highest version, in the running kernel's
// spelling as closely as the package name allows.
func newestInstalledKernel(ctx context.Context) string {
	var versions []string
	if _, err := lookPath("dpkg-query"); err == nil {
		out, verr := run(ctx, "dpkg-query", "-W", "-f", "${Package}\n", "linux-image-*")
		if verr == nil {
			for _, line := range lines(out) {
				if v := strings.TrimPrefix(line, "linux-image-"); v != line && !strings.HasPrefix(v, "generic") && !strings.HasPrefix(v, "amd64") && strings.ContainsAny(v, "0123456789") {
					versions = append(versions, v)
				}
			}
		}
	} else if _, err := lookPath("rpm"); err == nil {
		out, verr := run(ctx, "rpm", "-q", "kernel", "--qf", "%{VERSION}-%{RELEASE}.%{ARCH}\n")
		if verr == nil {
			versions = append(versions, lines(out)...)
		}
	}
	if len(versions) == 0 {
		return ""
	}
	sort.Slice(versions, func(i, j int) bool { return semverLess(versions[j], versions[i]) })
	return versions[0]
}

func runOS(ctx context.Context, req plugin.Request) (view.View, error) {
	st := osReader(ctx)
	p := plugin.NewPage(ctx, req)
	p.PutAs("state", "state", osStatePairs(st))
	if len(st.Updates) > 0 {
		p.PutAs("updates", "updates", updatesTable(st.Updates, true))
	}
	return p.View(), nil
}

// updatesTable is the updates the OS offers, saying of each whether
// installing it restarts the machine.
func updatesTable(updates []osUpdate, labels bool) view.Table {
	t := view.Table{Columns: []view.Column{{Name: "Update"}, {Name: "Version"}, {Name: "Restarts"}}}
	if labels {
		t.Columns = append(t.Columns, view.Column{Name: "Label"})
	}
	for _, u := range updates {
		restarts := "no"
		if u.Restart {
			restarts = "yes"
		}
		row := []view.Cell{view.Plain(u.Title), view.Plain(u.Version), view.Plain(restarts)}
		if labels {
			row = append(row, view.Plain(u.Label))
		}
		t.Add(row...)
	}
	t.Total = len(t.Rows)
	return t
}

// osStatePairs is the OS's lines of a page: what it offers, or the kernel, then
// whether a reboot is owed where the OS says.
func osStatePairs(st osState) view.KeyValue {
	pairs := osFacts(st)
	if p, ok := rebootPair(st); ok {
		pairs = append(pairs, p)
	}
	return view.KeyValue{Pairs: pairs}
}

// osFacts are the OS's lines other than the reboot: the updates it offers on
// macOS and the kernel on Linux. A read that failed says so in the line that
// would have answered, since "none offered" is a claim about a machine nobody
// looked at; the failure is the line, and not a note beside a claim it
// contradicts.
func osFacts(st osState) []view.Pair {
	if len(st.Notes) > 0 {
		return []view.Pair{{Key: "os updates", Value: "unreadable — " + strings.Join(st.Notes, "; ")}}
	}
	var pairs []view.Pair
	switch runtime.GOOS {
	case "darwin":
		pairs = append(pairs, view.Pair{Key: "os updates", Value: offered(st.Updates)})
	case "linux":
		if st.KernelRunning != "" {
			pairs = append(pairs, view.Pair{Key: "kernel running", Value: st.KernelRunning})
		}
		if st.KernelNewest != "" {
			newest := st.KernelNewest
			if st.kernelBehind() {
				newest += " — newer than the one running"
			}
			pairs = append(pairs, view.Pair{Key: "kernel installed", Value: newest})
		}
	}
	return pairs
}

// quiet reports whether the OS has nothing to say besides the reboot: no update
// offered, no kernel behind, and a read that worked.
func (s osState) quiet() bool {
	return len(s.Notes) == 0 && len(s.Updates) == 0 && !s.kernelBehind()
}

// offered is how many updates the OS offers, and how many of them restart
// the machine once installed.
func offered(updates []osUpdate) string {
	restarts := 0
	for _, u := range updates {
		if u.Restart {
			restarts++
		}
	}
	text := countText(len(updates), "offered")
	switch restarts {
	case 0:
		return text
	case 1:
		return text + ", 1 restarts the machine"
	}
	return text + ", " + itoa(restarts) + " restart the machine"
}

// rebootPair is whether a reboot is owed, or that the OS could not be read to
// say: "ok" is a claim that somebody looked. An OS that keeps no record of a
// reboot owed has no line at all (osState.RebootKnown).
func rebootPair(st osState) (view.Pair, bool) {
	switch {
	case !st.RebootKnown:
		return view.Pair{}, false
	case len(st.Notes) > 0:
		return view.Pair{Key: "reboot", Value: "unknown"}, true
	case st.RebootRequired:
		return view.Pair{Key: "reboot", Value: "pending reboot — " + st.RebootReason}, true
	}
	return view.Pair{Key: "reboot", Value: "ok"}, true
}

func countText(n int, what string) string {
	if n == 0 {
		return "none " + what
	}
	if n == 1 {
		return "1 " + what
	}
	return itoa(n) + " " + what
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
