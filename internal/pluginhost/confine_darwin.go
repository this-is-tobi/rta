package pluginhost

import (
	"fmt"
	"os/exec"
	"strings"
)

// Confinement on macOS is sandbox-exec wrapping the plugin's argv.
//
// sandbox-exec is documented as deprecated and has been since 10.8. It is
// also what Chrome, Firefox and every Homebrew sandbox actually use, it ships
// on every supported macOS, and the alternative is nothing. Deprecated and
// working beats undeprecated and absent; if Apple removes it, this returns an
// error and the platform joins Linux in reporting itself unconfined, which is
// a one-line change and an honest one.

const confined = true

// profile renders the SBPL policy.
//
// (allow default) first, then deny forms, because SBPL is last-match-wins:
// the denials must come after the blanket allow or they are overridden by it.
// That ordering is the whole security property of this string, which is also
// why validate() refuses any path that could close a form early — see
// denyset.go.
func profile(d DenySet) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	if len(d.NoAccess) > 0 {
		b.WriteString("(deny file-read* file-write*\n")
		for _, p := range d.NoAccess {
			fmt.Fprintf(&b, "  (subpath %q)\n", p)
		}
		b.WriteString(")\n")
	}
	if len(d.NoRead) > 0 {
		b.WriteString("(deny file-read*\n")
		for _, p := range d.NoRead {
			fmt.Fprintf(&b, "  (subpath %q)\n", p)
		}
		b.WriteString(")\n")
	}
	// **A rule that names a path stops applying when the path stops having
	// that name.** Renaming is the operation that does it, and in SBPL a
	// rename needs unlink permission on what is being renamed — so this is the
	// verb that makes every rule above hold: without it `mv ~/.ssh ~/x`
	// succeeds and every key is readable one command later, at a path no rule
	// mentions. The whole read-denied tier was worth nothing.
	//
	// Two kinds of filter, deliberately. `subpath` over the read-denied set,
	// so nothing inside it can be moved out either. `literal` over the
	// ancestors, because those directories must stay put while everything
	// inside them stays ordinary — `~` is on that list, and denying writes
	// under a subpath rooted there would deny most of what a plugin does.
	//
	// NoAccess needs neither: file-write* already contains file-write-unlink.
	if len(d.NoRead) > 0 || len(d.NoMove) > 0 {
		b.WriteString("(deny file-write-unlink\n")
		for _, p := range d.NoRead {
			fmt.Fprintf(&b, "  (subpath %q)\n", p)
		}
		for _, p := range d.NoMove {
			fmt.Fprintf(&b, "  (literal %q)\n", p)
		}
		b.WriteString(")\n")
	}
	return b.String()
}

// wrap returns the argv that runs cmd under the deny set.
//
// Inline -p, never -f. A profile written to a temp file and unlinked after
// cmd.Start() loses the race 200 times out of 200, because Start returns
// after fork+exec and before sandbox-exec has opened the file — and under a
// fail-closed rule that is 100% spawn failure on the one platform this
// decision relies on. Leaving the file behind instead trades that for a
// world-readable description of what rta protects, plus a cleanup problem on
// every crash. Inline has neither.
//
// The profile is an argument, not an environment variable and not stdin:
// stdin belongs to the plugin protocol, and an environment
// variable would be inherited by everything the plugin itself spawns.
func wrap(d DenySet, name string, args []string) (string, []string) {
	argv := append([]string{"-p", profile(d), name}, args...)
	return "/usr/bin/sandbox-exec", argv
}

// available reports whether this machine can actually confine, so that a
// missing sandbox-exec is a clear refusal at spawn time rather than an
// exec failure that reads as "plugin not found".
func available() error {
	if _, err := exec.LookPath("/usr/bin/sandbox-exec"); err != nil {
		return fmt.Errorf("/usr/bin/sandbox-exec is not available, so a plugin cannot be confined: %w", err)
	}
	return nil
}

// Confined reports whether this platform confines plugin processes, so that
// `rta doctor` can say so per machine rather than implying a guarantee that
// holds on one OS out of three.
func Confined() bool { return confined }
