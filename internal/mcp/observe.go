package mcp

import (
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// The observation listener: probes and counters, on an address that is never
// the MCP one.
//
// **Why a second listener rather than four more paths on the MCP port.**
// remote.go states that bearer authentication wraps the whole protocol handler
// and cross-origin protection wraps that, and that neither is optional or
// bypassable by a flag. Open paths on that listener would make the sentence
// false — its security would become a property of route matching, and every
// future handler added there a chance to get the matching wrong. Kept apart,
// that invariant stays exactly as written, and an operator binds this
// somewhere the agent-facing port is not: loopback, or a pod port the Service
// never publishes.
//
// **Why /metrics is still behind the bearer wall even here.** The counters are
// derived from the record: which agent called what, how often, how often it
// was refused. That is a map of the machine's activity, and "the operator will
// bind it somewhere safe" is the assumption that goes wrong. Binding is the
// outer control and the token is the inner one; a scrape config carries a
// bearer token without complaint, so the cost of keeping both is nothing.
//
// The probes are open, and that is deliberate rather than an oversight: they
// answer with a status code and a word, they read no record and name no agent,
// and a liveness probe that needs a credential is one more thing to get wrong
// at three in the morning.

// ObserveConfig configures the observation listener. Every field is optional;
// what is nil is simply not served, so a caller that wants probes and no
// counters passes no Verifier and no Metrics.
type ObserveConfig struct {
	// Verifier guards /metrics. With no Verifier there is nothing to check a
	// caller against, so /metrics is not served at all — an endpoint that
	// cannot authenticate anybody must not answer everybody.
	Verifier auth.TokenVerifier
	// Ready reports whether the server can actually serve: the store opens,
	// the data directory is writable. nil means nothing was configured to
	// check, and readiness answers yes rather than inventing a verdict.
	Ready func() error
	// Metrics renders the Prometheus exposition text. nil means /metrics is
	// not served.
	Metrics func() (string, error)
}

// NewObserveHandler builds the observation mux.
func NewObserveHandler(cfg ObserveConfig) http.Handler {
	mux := http.NewServeMux()

	// Liveness deliberately consults nothing. It answers whether this process
	// is still serving, and that is all it is allowed to mean: a liveness
	// probe wired to the store would ask Kubernetes to restart a process
	// because a volume detached, and the restarted process meets the same
	// detached volume. Readiness is where a broken dependency belongs.
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	ready := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if cfg.Ready != nil {
			if err := cfg.Ready(); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				// The reason, in the body, because this one is read by a
				// person looking at `kubectl describe` after the pod stopped
				// taking traffic — and because it says what is wrong with this
				// server's own storage, not anything about a caller.
				fmt.Fprintln(w, err.Error())
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	}
	mux.HandleFunc("GET /readyz", ready)
	// The older combined name, answering as the stricter of the two. Kept
	// because tooling and muscle memory both reach for it.
	mux.HandleFunc("GET /healthz", ready)

	if cfg.Metrics != nil && cfg.Verifier != nil {
		metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			body, err := cfg.Metrics()
			if err != nil {
				http.Error(w, "the record could not be read", http.StatusInternalServerError)
				return
			}
			// The exposition format's own content type, version included:
			// Prometheus accepts a bare text/plain, and naming the version is
			// what stops a future parser from guessing.
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			_, _ = w.Write([]byte(body))
		})
		// The same slow-failure backoff the MCP wall uses, for the same
		// reason: this address answers a guess as readily as a scrape, and a
		// guess per second should become a guess every two.
		verifier := slowFailures(cfg.Verifier, newBackoff(bearerFree, bearerWindow, bearerStep, bearerMax))
		mux.Handle("GET /metrics", auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{
			AllowMissingExpiration: true,
		})(metrics))
	}

	// Cross-origin protection wraps this the same way it wraps the MCP
	// listener. A browser page that can reach the address must not be able to
	// drive it on its holder's behalf, and that argument does not weaken
	// because the paths are smaller.
	return http.NewCrossOriginProtection().Handler(mux)
}
