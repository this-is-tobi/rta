package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The record, as numbers a dashboard already knows how to draw.
//
// The Grafana stack answers this in two halves and rta has to meet both.
// **Loki takes the lines** — `agent log --after <seq> -o json` is already a
// cursor over an append-only record, which is exactly what a log shipper
// wants, and needs nothing here. **Prometheus takes the counters**, and that
// is what this is: one command, the standard text exposition format, written
// into node_exporter's textfile collector by a timer. No listener, no port,
// no scrape endpoint in a process that can read your secret store.
//
// Everything below is derived rather than kept. rta stores no counters — the
// ledger is the only state — so a number here is a number somebody could
// recompute from the record, which is the property that makes a security
// metric worth alerting on.

// runMetrics renders the exposition format.
//
// The cost is one pass to count and one to verify, over the retained record
// rather than a window: a counter computed from the last N entries is not a
// counter. Retention bounds that at eight segments of eight megabytes, and
// this is a command a timer runs rather than an endpoint anybody scrapes.
func runMetrics(_ context.Context, _ plugin.Request) (view.View, error) {
	entries, err := agentlog.Read(0)
	if err != nil {
		return nil, view.Errorf("agent.metrics.unreadable", "%v", err)
	}
	rep, verifyErr := agentlog.Verify()

	var b strings.Builder
	// **Total is Retired + Entries, and that is the whole reason it is a
	// counter.** Counting the retained record alone would fall every time
	// retention drops a segment; Prometheus reads a fall as a process restart
	// and the graph loses the gap. The ledger already tracks what it retired,
	// so the true total survives rotation.
	total := callsEver(rep)
	if verifyErr != nil {
		// No verdict on the record means no retired count either, so the best
		// available answer is what is in front of us — an undercount, and one
		// that only happens when the record is already broken.
		total = float64(len(entries))
	}
	metric(&b, "rta_agent_calls_total", "counter",
		"Calls that arrived over MCP, including any the record has since retired.",
		[]sample{{value: total}})
	metric(&b, "rta_agent_calls_retired_total", "counter",
		"Calls dropped by the record's own retention, so the gap below is visible.",
		[]sample{{value: float64(rep.Retired)}})

	// Per-label counts are over the retained record and will step down when
	// retention drops a segment. Prometheus reads that as a counter reset and
	// keeps rate() honest across it, which is the standard behaviour and the
	// reason these are counters rather than gauges.
	metric(&b, "rta_agent_calls_recorded_total", "counter",
		"Calls still in the record, by capability, agent, outcome and how it was authorized.",
		callSamples(entries))

	waiting, _ := consent.Pending()
	metric(&b, "rta_agent_pending", "gauge",
		"Calls parked right now, waiting for a person to allow or deny them.",
		[]sample{{value: float64(len(waiting))}})

	metric(&b, "rta_grants_active", "gauge",
		"Grants in force right now, by capability and agent.", grantSamples())

	// The one worth alerting on. A record that stops verifying is either a
	// bug or somebody editing it, and both are things to find out about
	// within minutes rather than at the next review.
	intact := 1.0
	if verifyErr != nil || rep.Broken != 0 {
		intact = 0
	}
	metric(&b, "rta_record_intact", "gauge",
		"1 when the record's hash chain verifies end to end, 0 when it does not.",
		[]sample{{value: intact}})
	metric(&b, "rta_record_bytes", "gauge",
		"Size of the record on disk, across every segment.",
		[]sample{{value: float64(rep.Size)}})
	metric(&b, "rta_record_segments", "gauge",
		"How many files the record is spread over.",
		[]sample{{value: float64(rep.Files)}})

	return view.Text{Body: b.String()}, nil
}

// sample is one series: its labels and its value.
type sample struct {
	labels []label
	value  float64
}

type label struct{ name, value string }

// metric writes one metric family — HELP, TYPE, then its series, in the order
// the exposition format requires them.
//
// A family with no series still writes its HELP and TYPE. An empty result is
// a fact ("no grants are active"), and a dashboard panel that goes blank
// because a series vanished looks identical to one that broke.
func metric(b *strings.Builder, name, kind, help string, samples []sample) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
	for _, s := range samples {
		b.WriteString(name)
		if len(s.labels) > 0 {
			parts := make([]string, 0, len(s.labels))
			for _, l := range s.labels {
				parts = append(parts, l.name+`="`+escapeLabel(l.value)+`"`)
			}
			b.WriteString("{" + strings.Join(parts, ",") + "}")
		}
		fmt.Fprintf(b, " %g\n", s.value)
	}
}

// escapeLabel applies the exposition format's own escaping.
//
// Necessary and not decorative: an agent name is a word the operator typed,
// a capability id comes from a plugin, and either could carry a quote or a
// backslash. One unescaped quote does not corrupt one series, it makes the
// whole file unparseable and the dashboard goes blank.
func escapeLabel(v string) string {
	r := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n")
	return r.Replace(v)
}

// callSamples counts the retained record by the four labels worth splitting
// on, in a stable order so two runs a second apart produce the same file.
func callSamples(entries []agentlog.Entry) []sample {
	type key struct{ cap, agent, outcome, auth string }
	counts := map[key]int{}
	for _, e := range entries {
		counts[key{e.Cap, e.Agent, string(e.Outcome), string(e.Auth)}]++
	}
	keys := make([]key, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.cap != b.cap {
			return a.cap < b.cap
		}
		if a.agent != b.agent {
			return a.agent < b.agent
		}
		if a.outcome != b.outcome {
			return a.outcome < b.outcome
		}
		return a.auth < b.auth
	})
	out := make([]sample, 0, len(keys))
	for _, k := range keys {
		out = append(out, sample{labels: []label{
			{"capability", k.cap}, {"agent", k.agent},
			{"outcome", k.outcome}, {"authorized", k.auth},
		}, value: float64(counts[k])})
	}
	return out
}

// grantSamples counts what is in force now. Expired grants are not a series:
// they authorize nothing, and a gauge that kept them would report reach that
// does not exist.
func grantSamples() []sample {
	grants, verr := grant.Load()
	if verr != nil {
		return nil
	}
	type key struct{ target, agent string }
	counts := map[key]int{}
	now := time.Now()
	for _, g := range grants {
		if !g.Active(now) {
			continue
		}
		counts[key{g.Target, g.Agent}]++
	}
	keys := make([]key, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].target != keys[j].target {
			return keys[i].target < keys[j].target
		}
		return keys[i].agent < keys[j].agent
	})
	out := make([]sample, 0, len(keys))
	for _, k := range keys {
		out = append(out, sample{
			labels: []label{{"capability", k.target}, {"agent", k.agent}},
			value:  float64(counts[k]),
		})
	}
	return out
}

// callsEver is the counter that has to survive retention.
//
// Its own function because it is one addition that is easy to get wrong in
// the direction nothing notices: counting only what the record still holds
// gives the right answer until the day a segment is dropped, and then falls.
// Prometheus reads a fall as a process restart and the graph loses the gap.
func callsEver(rep agentlog.Report) float64 {
	return float64(rep.Retired + int64(rep.Entries))
}
