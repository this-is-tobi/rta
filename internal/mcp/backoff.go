package mcp

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// backoff slows a caller that keeps failing the same way, and it exists for
// what a failure costs the operator rather than the caller.
//
// Two places use it. A refused tool call is a row in the record — it has to
// be, refusals are the half an operator most wants back — and rows are what
// retention counts: at a few hundred bytes each, an agent looping on a tool
// that refuses it can push the whole 64 MB of real history out of retention
// in minutes, and the retired anchors would be all that was left saying so.
// And a rejected bearer token costs one constant-time compare, cheap enough
// that the 16-character floor was the only thing between a listener on the
// network and a guess a second forever.
//
// Neither is a cap. The first `free` failures in a window cost nothing,
// because an agent probing a catalogue of ninety tools legitimately collects
// a dozen refusals before it learns what it holds, and an operator mistypes
// a token. Past that, each further failure waits step, then twice that, up
// to max, and a window with no failures forgets the caller. A caller that
// reads its refusals never meets the delay; a loop meets a wall that grows,
// which turns minutes of churn into weeks and a guess a second into one
// every two.
//
// Keyed by what the caller cannot change cheaply: the credential on the HTTP
// transport, the server's own session over stdio, the remote address for a
// bearer that has not authenticated yet. Behind a reverse proxy every client
// shares one address, so a guessing attacker slows the operators beside it —
// by at most max, only while the guessing lasts. That trade is taken and
// documented rather than avoided by trusting a Forwarded header the attacker
// writes.
type backoff struct {
	free   int
	window time.Duration
	step   time.Duration
	max    time.Duration
	now    func() time.Time

	mu      sync.Mutex
	strikes map[string]*strike
}

type strike struct {
	count int
	last  time.Time
}

// maxKeys bounds what is remembered. A caller idle for a window is dropped
// to make room; a flood of distinct addresses inside one window is not
// tracked past this, and goes unslowed. That is the honest limit: a guesser
// with thousands of addresses is what the token floor stands against, and a
// map that grows with every address seen would be a cheaper attack than
// the one this slows.
const maxKeys = 4096

const (
	refusalFree   = 20
	refusalWindow = time.Minute
	refusalStep   = 250 * time.Millisecond
	refusalMax    = 5 * time.Second

	bearerFree   = 5
	bearerWindow = time.Minute
	bearerStep   = 250 * time.Millisecond
	bearerMax    = 2 * time.Second
)

func newBackoff(free int, window, step, max time.Duration) *backoff {
	return &backoff{free: free, window: window, step: step, max: max, now: time.Now, strikes: map[string]*strike{}}
}

// delay records one failure for key and returns how long that failure
// should take to answer.
func (b *backoff) delay(key string) time.Duration {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	s := b.strikes[key]
	if s == nil {
		if len(b.strikes) >= maxKeys {
			b.prune(now)
		}
		if len(b.strikes) >= maxKeys {
			return 0
		}
		s = &strike{}
		b.strikes[key] = s
	}
	if now.Sub(s.last) > b.window {
		s.count = 0
	}
	s.count++
	s.last = now
	over := s.count - b.free
	if over <= 0 {
		return 0
	}
	// Doubling from step; past twenty doublings the shift would overflow
	// long after the cap has been reached.
	if over > 20 {
		return b.max
	}
	d := b.step << (over - 1)
	if d > b.max {
		return b.max
	}
	return d
}

func (b *backoff) prune(now time.Time) {
	for key, s := range b.strikes {
		if now.Sub(s.last) > b.window {
			delete(b.strikes, key)
		}
	}
}

// hold records one failure for key and waits out its delay, or less if the
// caller goes away first.
func (b *backoff) hold(ctx context.Context, key string) {
	d := b.delay(key)
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// refusalKey is who a refused call is charged to: the credential when the
// transport has one, the server's own session otherwise — over stdio one
// process is one client, so the session is the caller.
func refusalKey(ctx context.Context, opts Options) string {
	if cred := credentialName(ctx); cred != "" {
		return cred
	}
	return opts.Session
}

// slowFailures wraps a verifier so that a rejected token from one address
// is answered slower each time, after the first few. The verifier's own
// answer is unchanged: the same generic error, later.
func slowFailures(v auth.TokenVerifier, b *backoff) auth.TokenVerifier {
	return func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		info, err := v(ctx, token, req)
		if err != nil {
			b.hold(ctx, remoteHost(req))
		}
		return info, err
	}
}

// remoteHost is the address the connection actually came from, port
// stripped. Never a Forwarded header: it is the caller's to write.
func remoteHost(req *http.Request) string {
	if req == nil {
		return ""
	}
	if host, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		return host
	}
	return req.RemoteAddr
}
