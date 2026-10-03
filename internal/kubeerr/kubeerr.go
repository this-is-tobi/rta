// Package kubeerr reads what kubectl says when it fails, for the places in
// rta that drive it: the forward and the secret read in internal/tunnel and the
// kube.* audits. Each is told by a person's mistake or a cluster's state in its
// own words, and one of the stderr shapes — a cluster that did not answer — is
// a klog line that none of them should put in front of a reader.
package kubeerr

import (
	"regexp"
	"strings"
)

var (
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
