package pluginhost

import (
	"os"
	"strings"
)

// allowedEnv is what a plugin process inherits. Everything else is dropped.
//
// go-plugin's SkipHostEnv exists and defaults to false, so the default is that
// a plugin gets the host's entire environment: every RTA_* variable, every
// cloud credential the user exported into that shell, every token their
// direnv put there. A plugin that never reads a file could exfiltrate an
// AWS session by starting up.
//
// This is an allowlist of *names*, and each one is here for a stated reason
// rather than because it looked harmless:
//
//   - PATH — a plugin that shells out needs to find what it shells out to.
//   - HOME — the plugin's own config lives under it; dropping it makes
//     os.UserHomeDir fail and libraries fall back to "/" or to the invoking
//     user's records in unpredictable ways.
//   - TMPDIR — dropping this is a real security regression, not a saving. Go's
//     os.CreateTemp("", …) falls back to /tmp, which on macOS is mode 1777 and
//     shared, while $TMPDIR is a per-user 0700 directory. Every plugin that
//     writes a temp file would move its scratch data somewhere every other
//     user on the box can see it.
//   - TZ, LANG, LC_* — a plugin that formats a timestamp or sorts a list in a
//     different locale than the surface around it looks broken.
//   - SSL_CERT_FILE, SSL_CERT_DIR — corporate CA bundles. Without these, every
//     plugin that makes an HTTPS request fails on exactly the networks most
//     likely to be running this, with an error about certificate verification
//     that names nothing the user can act on.
//
// go-plugin's own handshake variables are added by go-plugin itself after this
// list is applied, so they are deliberately absent here.
var allowedEnv = []string{
	"PATH",
	"HOME",
	"TMPDIR",
	"TZ",
	"LANG",
	"SSL_CERT_FILE",
	"SSL_CERT_DIR",
}

// allowedEnvPrefixes covers the families where the name is not fixed. LC_ has
// a dozen members (LC_ALL, LC_TIME, LC_COLLATE, …) and enumerating them is a
// list that goes stale.
var allowedEnvPrefixes = []string{"LC_"}

// childEnv builds the environment for a plugin process from the host's.
//
// It filters rather than constructing from scratch so that a variable present
// in the host with an empty value crosses as empty rather than as absent —
// those mean different things to a surprising number of programs.
func childEnv(hostEnv []string) []string {
	allowed := make(map[string]bool, len(allowedEnv))
	for _, name := range allowedEnv {
		allowed[name] = true
	}
	out := make([]string, 0, len(allowedEnv)+2)
	for _, kv := range hostEnv {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if allowed[name] {
			out = append(out, kv)
			continue
		}
		for _, p := range allowedEnvPrefixes {
			if strings.HasPrefix(name, p) {
				out = append(out, kv)
				break
			}
		}
	}
	return out
}

// Environ is childEnv over this process's environment.
func Environ() []string { return childEnv(os.Environ()) }
