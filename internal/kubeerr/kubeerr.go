// Package kubeerr reads what kubectl says when it fails, for the places in
// rta that drive it: the forward and the secret read in internal/tunnel and the
// kube.* audits. Each is told by a person's mistake or a cluster's state in its
// own words, and one of the stderr shapes — a cluster that did not answer — is
// a klog line that none of them should put in front of a reader.
package kubeerr

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// execPluginMissing matches kubectl's two spellings of an exec credential
	// plugin this machine does not have: a bare name looked up on $PATH, and an
	// absolute path that is not there. Both captured from a real kubectl.
	execPluginMissing = regexp.MustCompile(
		`exec: (?:executable (\S+) not found|fork/exec (\S+): no such file or directory)`)
	// apiServerURL is the API server in the current spelling of a kubectl
	// failure: the scheme and the host, up to the first path or query.
	apiServerURL = regexp.MustCompile(`https?://[^\s"\\/?]+`)
	// dialedAddress is the same server in the old one, which names only the
	// address the dial went to.
	dialedAddress = regexp.MustCompile(`dial tcp (\S+?):\s`)
	// unreachableWhy is how a dial that never connected says so, in the words
	// Go's net package and kubectl's own transport use for it.
	unreachableWhy = regexp.MustCompile(
		`connection refused|i/o timeout|no such host|no route to host|network is unreachable|TLS handshake timeout|context deadline exceeded`)
)

// Server is where kubectl was dialling when it failed, or "".
func Server(stderr string) string {
	if url := apiServerURL.FindString(stderr); url != "" {
		return url
	}
	if m := dialedAddress.FindStringSubmatch(stderr); m != nil {
		return m[1]
	}
	return ""
}

// Unreachable is why kubectl could not reach the API server, in the words it
// used, or "" when its stderr is not about that. The two spellings of it are
// the old one, "Unable to connect to the server: …", and the current one, a
// klog line about failing to discover the API groups with the dial error
// inside it — a timestamp, a process id and a source file around the one
// clause that matters. Both carry the reason itself.
func Unreachable(stderr string) string {
	if !strings.Contains(stderr, "dial tcp") && !strings.Contains(stderr, "Unable to connect to the server") {
		return ""
	}
	return unreachableWhy.FindString(stderr)
}

// NotAuthenticated reports whether kubectl's stderr is the API server's 401:
// the request reached it and it did not know who was asking. That is a login
// the cluster did not accept, which is a refused credential, and the same login
// is refused again at the next try. It is about identity rather than permission,
// which is the API server's 403 and a different answer.
//
// Not the credential plugin failing: nothing reached the server then, and
// CredentialHelperFailed says so.
func NotAuthenticated(stderr string) bool {
	return containsAny(stderr,
		"Unauthorized",
		"You must be logged in to the server",
		"the server has asked for the client to provide credentials",
	)
}

// CredentialHelperFailed reports whether kubectl's stderr is the credential
// helper of the context failing before there was a request to make: an exec
// plugin that exited with an error, one kubectl cannot use, or an auth provider
// it no longer ships.
//
// **Nothing asked the cluster, so the cluster refused nothing**, however the
// helper words its failure: it cannot be a permissions answer, and it is not a
// login refused either. It is a credential that is not there, and it is there
// again the moment the person has logged in, which a dashboard tile can only
// notice by asking again. Read it ahead of NotAuthenticated, since a helper that
// reached an identity provider and was refused there says "Unauthorized" in its
// own message.
func CredentialHelperFailed(stderr string) bool {
	return containsAny(stderr,
		"getting credentials", // kubectl's own wrapper around an exec plugin
		"exec plugin",         // "exec plugin: invalid apiVersion", and friends
		"credential plugin",
		"no Auth Provider found",
	)
}

// CredentialPluginMissing reports which credential plugin the kubeconfig names
// and this machine lacks, when that is what stderr says.
//
// Read ahead of every other classification, because the message carries
// "not found" and, read after the fact, became three different wrong answers: a
// missing service on the forward, a missing secret on the read, and "authenticate
// again" on a listing — each sending somebody to fix a thing that is fine, for a
// tool they do not have. The kube plugin met the same message and draws the same
// line.
func CredentialPluginMissing(stderr string) (string, bool) {
	m := execPluginMissing.FindStringSubmatch(stderr)
	if m == nil {
		return "", false
	}
	exe := m[1]
	if exe == "" {
		exe = m[2]
	}
	return filepath.Base(exe), true
}

func containsAny(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}
