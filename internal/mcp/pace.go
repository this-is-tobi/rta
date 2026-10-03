package mcp

import (
	"context"
	"sync"
	"time"
)

// pacer is what keeps a caller whose calls all succeed from writing the
// record's history away.
//
// The refusal backoff (backoff) stops a loop on a tool that refuses it, and
// a call that succeeds is a row too: a scripted client calling a free read as
// fast as the transport carried it wrote some 1,800 rows a second, and the 64
// MB the record keeps was gone in about two minutes — everything an operator
// could have looked back on replaced by the loop's own rows, with retired
// anchors left to say that it had happened. A refusal's count-and-double does
// not fit here, because a session of honest calls is long and a count that
// forgets only after a quiet minute would slow it for good.
//
// So a token bucket: every caller holds up to burst calls in hand, regained at
// rate a second, and a call finding the bucket empty waits for the token it
// owes. An agent working a task, whose calls come seconds apart because a
// model sits between them, never meets it, and neither does a script that
// stays under the rate; a loop is held to the rate, which turns minutes of
// churn into hours.
//
// **The wait comes before the call runs, and at most queue calls of one caller
// wait at once.** A wait after the row was written held the reply and nothing
// else: a client that sends its calls without reading the replies has every
// one of them running, and writing its row, at once (the server runs each in
// a goroutine of its own), so the rate held a sequential loop and let a
// pipelined one through at whatever the record's lock allowed, some 130 rows a
// second. Waiting first holds both. The calls past queue wait for a place
// before they take a token, which is what keeps a flood from owing more than
// queue/rate seconds and then being let through all together: the debt is
// real, repaid at the rate, and bounded by how many may be in it. A call that
// is given up on while it waits (its client went away, the server is stopping)
// has not run and is not a row of its own beyond the refusal that says so.
//
// Keyed as refusals are: the credential over HTTP, the server's own session
// over stdio.
type pacer struct {
	burst float64
	rate  float64
	queue int
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
	// waiting holds a place for each call of this caller that is waiting out
	// what it owes.
	waiting chan struct{}
}

const (
	// openBurst and openRate are what the calls that need no grant are held
	// to: 600 in hand, 5 a second after. Read-heavy sessions the catalogue
	// supports — a tree walked, a log paged through, a cluster listed
	// namespace by namespace — are tens to a few hundred calls a minute.
	openBurst = 600
	openRate  = 5
	// openQueue is how many calls of one caller may wait at once: the longest
	// a call waits is openQueue/openRate, three seconds and a fifth.
	openQueue = 16
)

func newPacer(burst int, rate float64, queue int) *pacer {
	return &pacer{burst: float64(burst), rate: rate, queue: queue, now: time.Now, buckets: map[string]*bucket{}}
}

// bucketFor is the caller's bucket, a full one for a caller not seen, or nil
// when too many callers are remembered to remember another: one that cannot be
// told apart from the rest is not held up for them.
func (p *pacer) bucketFor(key string) *bucket {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := p.buckets[key]
	if b != nil {
		return b
	}
	now := p.now()
	if len(p.buckets) >= maxKeys {
		p.prune(now)
	}
	if len(p.buckets) >= maxKeys {
		return nil
	}
	b = &bucket{tokens: p.burst, at: now, waiting: make(chan struct{}, p.queue)}
	p.buckets[key] = b
	return b
}

// take takes one token from b and returns how long the call that took it is to
// wait: nothing while the bucket has one, and the time the debt takes to repay
// at rate once it does not.
func (p *pacer) take(b *bucket) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	b.tokens += now.Sub(b.at).Seconds() * p.rate
	if b.tokens > p.burst {
		b.tokens = p.burst
	}
	b.at = now
	b.tokens--
	if b.tokens >= 0 {
		return 0
	}
	return time.Duration(-b.tokens / p.rate * float64(time.Second))
}

// prune drops the callers whose bucket has refilled: a caller idle long
// enough to be full is a caller the next call finds the same way a new one
// would be found.
func (p *pacer) prune(now time.Time) {
	for key, b := range p.buckets {
		if len(b.waiting) == 0 && b.tokens+now.Sub(b.at).Seconds()*p.rate >= p.burst {
			delete(p.buckets, key)
		}
	}
}

// wait takes a token for key and waits out what it owes, and says why not when
// the caller goes away first.
func (p *pacer) wait(ctx context.Context, key string) error {
	if p == nil {
		return nil
	}
	b := p.bucketFor(key)
	if b == nil {
		return nil
	}
	select {
	case b.waiting <- struct{}{}:
		defer func() { <-b.waiting }()
	case <-ctx.Done():
		return ctx.Err()
	}
	d := p.take(b)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
