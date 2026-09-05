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
		Description: "On macOS, what `softwareupdate --list` offers and whether any of it restarts " +
			"the machine. On Linux, whether the system says a reboot is required " +
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
	Updates        []osUpdate
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

func readOS(ctx context.Context) osState {
	switch runtime.GOOS {
	case "darwin":
		return readMacOS(ctx)
	case "linux":
		return readLinux(ctx)
	default:
		return osState{Notes: []string{runtime.GOOS + " is not read here"}}
	}
}

// readMacOS parses `softwareupdate --list`:
//
//   - Label: macOS Sonoma 14.6.1-23G93
//     Title: macOS Sonoma 14.6.1, Version: 14.6.1, Size: 1234KiB, Recommended: YES, Action: restart,
func readMacOS(ctx context.Context) osState {
	var st osState
	out, _, verr := run(ctx, "softwareupdate", "--list")
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
	for _, u := range st.Updates {
		if u.Restart {
			st.RebootRequired, st.RebootReason = true, "an offered update restarts the machine"
			break
		}
	}
	return st
}

// readLinux reads the reboot flag the distribution family writes, and the
// kernel gap from the package database.
func readLinux(ctx context.Context) osState {
	var st osState
	if _, err := os.Stat("/var/run/reboot-required"); err == nil {
		st.RebootRequired, st.RebootReason = true, "/var/run/reboot-required is present"
	} else if _, err := lookPath("needs-restarting"); err == nil {
		// Exit 1 means a reboot is needed; 0 means not.
		if _, code, verr := run(ctx, "needs-restarting", "-r"); verr == nil && code == 1 {
			st.RebootRequired, st.RebootReason = true, "needs-restarting says so"
		}
	}
	if out, _, verr := run(ctx, "uname", "-r"); verr == nil {
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
		out, _, verr := run(ctx, "dpkg-query", "-W", "-f", "${Package}\n", "linux-image-*")
		if verr == nil {
			for _, line := range lines(out) {
				if v := strings.TrimPrefix(line, "linux-image-"); v != line && !strings.HasPrefix(v, "generic") && !strings.HasPrefix(v, "amd64") && strings.ContainsAny(v, "0123456789") {
					versions = append(versions, v)
				}
			}
		}
	} else if _, err := lookPath("rpm"); err == nil {
		out, _, verr := run(ctx, "rpm", "-q", "kernel", "--qf", "%{VERSION}-%{RELEASE}.%{ARCH}\n")
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
	if verr := supported(); verr != nil {
		return nil, verr
	}
	st := readOS(ctx)
	p := plugin.NewPage(ctx, req)
	p.PutAs("state", "state", osStatePairs(st))
	if len(st.Updates) > 0 {
		t := view.Table{Columns: []view.Column{
			{Name: "Update"}, {Name: "Version"}, {Name: "Restarts", Kind: view.KindStatus}, {Name: "Label"},
		}}
		for _, u := range st.Updates {
			restart := "no"
			if u.Restart {
				restart = "pending reboot"
			}
			t.Rows = append(t.Rows, []string{u.Title, u.Version, restart, u.Label})
		}
		t.Total = len(t.Rows)
		p.PutAs("updates", "updates", t)
	}
	return p.View(), nil
}

func osStatePairs(st osState) view.KeyValue {
	kv := view.KeyValue{}
	switch runtime.GOOS {
	case "darwin":
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "os updates", Value: countText(len(st.Updates), "offered")})
	case "linux":
		if st.KernelRunning != "" {
			kv.Pairs = append(kv.Pairs, view.Pair{Key: "kernel running", Value: st.KernelRunning})
		}
		if st.KernelNewest != "" {
			newest := st.KernelNewest
			if st.kernelBehind() {
				newest += " — newer than the one running"
			}
			kv.Pairs = append(kv.Pairs, view.Pair{Key: "kernel installed", Value: newest})
		}
	}
	reboot := "ok"
	if st.RebootRequired {
		reboot = "pending reboot — " + st.RebootReason
	}
	kv.Pairs = append(kv.Pairs, view.Pair{Key: "reboot", Value: reboot})
	for _, n := range st.Notes {
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "note", Value: n})
	}
	return kv
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
