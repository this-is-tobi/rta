// Package time is the built-in instant plugin: one capability that reads a
// timestamp in whatever form somebody has it and shows it in every form they
// might need next.
//
// It exists because the conversion is the part everybody gets wrong. An epoch
// number in a log line, a JWT's `exp`, a Kubernetes event's timestamp and a
// certificate's notAfter are all the same kind of fact spelled four ways, and
// answering "is that recent?" about any of them means either a second window
// with a converter in it or arithmetic done in the head — which is where the
// mistakes come from, in both directions. An agent is no better at it than a
// person; it is worse, because it will produce a confident wrong answer rather
// than reaching for a tool.
//
// Pure computation over its own argument, plus the machine's clock and zone.
// No network, no state, nothing to confine.
package time

import (
	"context"
	"strconv"
	"strings"
	stdtime "time"

	"github.com/this-is-tobi/rule-them-all/builtin/internal/timefmt"
	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Plugin returns the time plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "time",
		Summary: "Read an instant in every form worth having: epoch, UTC, local, a named zone",
		Capabilities: []plugin.Capability{
			{
				ID:      "time.at",
				Summary: "Convert an instant between epoch, UTC, local time and a named zone",
				// Not HostSpecific, though two of its rows come from this
				// machine. The answer to `time at 1516242622` is the same
				// everywhere, and the two rows that are not — `local`, and
				// what `now` resolves to — carry the zone they were rendered
				// in, so a remote caller reading the server's clock can see
				// that is what they are reading. HostSpecific is for answers
				// that mislead when the server is not where you are sitting;
				// a labelled zone does not.
				//
				// Being the plugin's only previewable capability makes this
				// its dashboard tile, and with `when` defaulting to now that
				// tile is a clock showing UTC beside local. That is a
				// deliberate thing for it to be, not a side effect: correlating
				// a log line's UTC stamp against the wall clock is the most
				// common reason to want any of this.
				Safety:     plugin.Read,
				Idempotent: true,
				// An RFC3339 stamp with its zone is 29 cells and does not
				// survive being wrapped — half of one is not a timestamp.
				MinWidth: 40,
				Description: "Takes an instant in whatever form you have it — an epoch number at any " +
					"precision, an RFC3339 stamp, a plain date, or a duration either side of now " +
					"(`90m ago`, `in 2h`, `+90m`) — and shows it as UTC, as local time, as epoch " +
					"seconds and milliseconds, and as how long ago or away it is. With --zone, in a " +
					"third timezone too.\n\n" +
					"A bare number carries no unit, so the unit is chosen by magnitude and then " +
					"stated back to you in the `read-as` row: between 1970 and 1973 the ranges " +
					"genuinely overlap and no rule can resolve that, which makes saying what was " +
					"assumed part of the answer rather than a footnote.",
				Inputs: []plugin.Field{
					{Name: "when", Type: plugin.String, Positional: true, Default: "now",
						Help: "an instant: now, an epoch number, 2026-09-04T12:00:00Z, 2026-09-04, or a relative duration like \"90m ago\""},
					{Name: "zone", Type: plugin.String,
						Help: "also show it in this IANA timezone (Europe/Paris)"},
				},
				Run: runAt,
			},
		},
	}
}

func runAt(_ context.Context, req plugin.Request) (view.View, error) {
	t, unit, err := resolve(strings.TrimSpace(req.String("when")), stdtime.Now())
	if err != nil {
		return nil, err
	}

	pairs := []view.Pair{
		{Key: "utc", Value: t.UTC().Format(stdtime.RFC3339)},
		{Key: "local", Value: withZone(t.In(stdtime.Local))},
	}

	if name := strings.TrimSpace(req.String("zone")); name != "" {
		loc, zoneErr := stdtime.LoadLocation(name)
		if zoneErr != nil {
			return nil, view.Errorf("time.at.zone", "%q is not a timezone rta could load: %v", name, zoneErr).
				WithHint("an IANA name like Europe/Paris, America/New_York or UTC")
		}
		pairs = append(pairs, view.Pair{Key: loc.String(), Value: withZone(t.In(loc))})
	}

	pairs = append(pairs,
		view.Pair{Key: "relative", Value: format.Ago(t)},
		view.Pair{Key: "epoch", Value: strconv.FormatInt(t.Unix(), 10)},
		view.Pair{Key: "epoch-ms", Value: strconv.FormatInt(t.UnixMilli(), 10)},
	)
	// Only when a unit was guessed. On every other input there was nothing to
	// assume, and a row saying so would be noise on the common path.
	if unit != "" {
		pairs = append(pairs, view.Pair{Key: "read-as", Value: string(unit)})
	}
	return view.KeyValue{Pairs: pairs}, nil
}

// resolve turns what somebody typed into an instant, and names the epoch unit
// when it had to pick one.
//
// Order is by how specific the form is, not by how likely it is: `now` is a
// word, an epoch is digits, and everything else is a layout. Nothing here
// matches two of those, so the order is for reading rather than for
// correctness — except the unsigned-duration case at the end, which is a
// refusal and has to come after every form that would have succeeded.
func resolve(raw string, now stdtime.Time) (stdtime.Time, timefmt.Unit, *view.Error) {
	if raw == "" || strings.EqualFold(raw, "now") {
		return now, "", nil
	}
	if d, ok := relativeDuration(raw); ok {
		return now.Add(d), "", nil
	}
	if t, unit, ok := timefmt.ParseEpoch(raw); ok {
		return t, unit, nil
	}
	if t, ok := timefmt.ParseInstant(raw, stdtime.Local); ok {
		return t, "", nil
	}
	// A length of time is not an instant, and guessing which end of now it
	// hangs off would be wrong half the time. This is separate from the
	// message below because "I do not understand that" is unhelpful when the
	// value is understood perfectly and only the direction is missing.
	if _, parseErr := stdtime.ParseDuration(raw); parseErr == nil {
		return stdtime.Time{}, "", view.Errorf("time.at.unsigned",
			"%q is a length of time, not an instant — in which direction?", raw).
			WithHint("say which: `" + raw + " ago`, or `in " + raw + "`")
	}
	return stdtime.Time{}, "", view.Errorf("time.at.unreadable",
		"%q is not an instant this understands", raw).
		WithHint("`now`, an epoch number (1516242622), a relative duration (`90m ago`, `in 2h`), " +
			"or an exact time (" + strings.Join(timefmt.Examples(now), ", ") + ")")
}

// relativeDuration reads the spellings that carry their own direction: `-90m`,
// `+90m`, `90m ago` and `in 90m`. A bare `90m` is not one of them, and resolve
// refuses it rather than picking a direction.
//
// The worded forms are not sugar. A CLI cannot be handed `-90m` as an argument
// without `--` in front of it — the leading dash is a flag to any parser that
// sees it first — so on the surface where this capability is most used, the
// signed spelling is the one with a trap in it and `90m ago` is the one that
// just works.
func relativeDuration(raw string) (stdtime.Duration, bool) {
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(raw, "+"), strings.HasPrefix(raw, "-"):
		d, err := stdtime.ParseDuration(raw)
		return d, err == nil
	case strings.HasSuffix(lower, " ago"):
		d, err := stdtime.ParseDuration(strings.TrimSpace(lower[:len(lower)-len(" ago")]))
		return -d, err == nil
	case strings.HasPrefix(lower, "in "):
		d, err := stdtime.ParseDuration(strings.TrimSpace(lower[len("in "):]))
		return d, err == nil
	}
	return 0, false
}

// withZone spells an instant with the abbreviation of the zone it is being
// shown in. RFC3339 carries the offset but not the name, and "+02:00" in
// summer and "+01:00" in winter are the same zone — the abbreviation is what
// says so.
func withZone(t stdtime.Time) string {
	name, _ := t.Zone()
	stamp := t.Format(stdtime.RFC3339)
	if name == "" {
		return stamp
	}
	return stamp + " " + name
}
