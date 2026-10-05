package app

import (
	"context"
	"io"
	"strings"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// What `rta init` did with the plan, and how it says so.

// initResult is the plan carried out, as the pairs that answer for it.
type initResult struct {
	pairs      []view.Pair
	registered []string
	failed     []string
	dryRun     bool
	// offered is how many things the plan put to the person or told them to
	// do. None is a machine that is set up, which is an answer worth giving.
	offered int
}

// run makes the registrations that were accepted, through the same call `rta
// mcp install` makes, and says what became of each. A refusal from one client
// does not stop the others: a person who answered yes to three is owed all
// three results, and the exit status says that one did not go through.
//
// said is where the client's own words go, as for the install command.
func (p initPlan) run(ctx context.Context, said io.Writer, accepted []bool, dryRun bool) initResult {
	res := initResult{dryRun: dryRun, offered: len(p.offers) + len(p.notes)}
	if len(p.already) > 0 {
		res.pairs = append(res.pairs, view.Pair{Key: "already registered", Value: strings.Join(p.already, ", ")})
	}
	for i, o := range p.offers {
		label := o.client.label
		if !accepted[i] {
			res.pairs = append(res.pairs, view.Pair{Key: "skipped",
				Value: label + " — `" + o.later() + "` registers it when you want"})
			continue
		}
		req := o.req
		req.dryRun = dryRun
		out, err := installClient(ctx, said, req)
		switch {
		case err != nil:
			res.failed = append(res.failed, o.client.name)
			res.pairs = append(res.pairs, view.Pair{Key: "failed", Value: label + " — " + view.AsError(err, "").Message})
		case out.block:
			res.failed = append(res.failed, o.client.name)
			res.pairs = append(res.pairs, view.Pair{Key: "failed", Value: label + " — " + answerValue(out.answer, "why") +
				"; `rta mcp install " + o.client.name + " --show` prints what to add by hand"})
		default:
			verb := out.answer.Pairs[0].Key
			if !dryRun {
				res.registered = append(res.registered, label)
			}
			where, _, _ := strings.Cut(answerValue(out.answer, "scope"), " — ")
			res.pairs = append(res.pairs, view.Pair{Key: verb, Value: label + ", " + where + " — `" + o.line + "`"})
		}
	}
	res.pairs = append(res.pairs, p.notes...)
	return res
}

// answer is the pairs, with what to do next and, when there was nothing to do
// at all, the sentence that says that is the good outcome.
func (r initResult) answer() view.KeyValue {
	pairs := append([]view.Pair(nil), r.pairs...)
	switch {
	case len(r.registered) > 0:
		pairs = append(pairs, view.Pair{Key: "next", Value: "restart " + strings.Join(r.registered, " and ") +
			", ask " + format.Plural(len(r.registered), "it", "one of them") +
			" to call sys_overview, and `rta agent overview` shows it connected"})
	case r.dryRun && len(r.pairs) > 0:
		pairs = append(pairs, view.Pair{Key: "next", Value: "run without --dry-run to do it"})
	}
	if r.offered == 0 {
		pairs = append(pairs, view.Pair{Key: "unchanged",
			Value: "rta is already configured as well as it can be without your choices"})
	}
	return view.KeyValue{Pairs: pairs}
}

// failure is the exit status of an init in which a registration the person
// asked for did not go through, once the answer has said which.
func (r initResult) failure() error {
	if len(r.failed) == 0 {
		return nil
	}
	return view.Errorf("core.init.failed", "rta could not register itself with %s", strings.Join(r.failed, ", ")).
		WithHint("the lines above say why; `rta mcp install <client>` runs the same registration on its own")
}
