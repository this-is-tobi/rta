package grant

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/builtin/internal/compact"
	core "github.com/this-is-tobi/rta/internal/grant"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// remoteList is `grant list --server <name>`: the same roster question asked
// of a remote rta server, as a signed operator call. The rows render through
// grantsTable exactly as local ones do — the operator is deciding the same
// things about them — but everything judged against local state stays out:
// staleness is the server's config's business, and the suppressed count and
// empty-case hints arrive from the server's own store rather than being
// recomputed against files that describe this machine. So does the verdict
// on each grant's plugin build, which the server judges against its own
// plugins (operator.GrantList.Artifacts) and this side only draws.
func remoteList(ctx context.Context, req plugin.Request, server string) (view.View, error) {
	if req.Bool("detail") {
		sf := req.Surface()
		return nil, view.Errorf("grant.remote.detail",
			"%s describes this machine's catalogue, not %s's", sf.InputName("detail"), server).
			WithHint("ask again " + sf.WithoutInputs("detail") + "; the reach tiers depend on flags the server " +
				"was started with")
	}
	base, verr := operatorid.ServerURL(server)
	if verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: "would read the grant roster from " + base + " as a signed " +
			"operator call — the passphrase is asked first"}, nil
	}
	pass, verr := operatorid.PromptSecret(req, false)
	if verr != nil {
		return nil, verr
	}
	signer, verr := operatorid.Unlock(pass)
	if verr != nil {
		return nil, verr
	}
	var gl operatorid.GrantList
	if verr := (operatorid.Client{URL: base, Signer: signer}).Call(ctx, operatorid.VerbGrantList, nil, &gl); verr != nil {
		return nil, verr
	}
	// --role narrows the roster here, on this side of the channel, as
	// heldTable narrows the local one: the list verb carries no selector,
	// and the whole roster under a request for one role's rows reads as if
	// the role held all of them.
	judged := remoteStates(gl)
	grants, states := gl.Grants, judged
	role, agent := strings.TrimSpace(req.String("role")), strings.TrimSpace(req.String("agent"))
	if role != "" || agent != "" {
		grants, states = nil, nil
		for i, g := range gl.Grants {
			if (role == "" || g.Role == role) && (agent == "" || g.Agent == agent) {
				grants = append(grants, g)
				states = append(states, judged[i])
			}
		}
	}
	// Each row's build as the server judged it, against its own plugins, and
	// the warnings beside the marks worded for that server — the local
	// roster's column and sentences, since the operator is deciding the same
	// things about these rows.
	t := grantsTable(grants, nil, states, false, compact.For(req))
	t.Warnings = append(t.Warnings, artifactWarnings(req.Surface(), grants, states, server)...)
	// The table even when the server holds nothing, with the sentence beside
	// it for a person (view.Table.Empty), as the local listing answers: the
	// sentence alone was a text view `jq '.rows[]'` could not iterate.
	switch {
	case len(grants) == 0 && len(gl.Grants) > 0:
		t.Empty = fmt.Sprintf("No standing grant on %s matches %s.", server, filterWords(role, agent))
	case len(grants) == 0:
		t.Empty = fmt.Sprintf("No active grants on %s — its agents can only read.", server)
	}
	if gl.Suppressed > 0 {
		return view.Sections{Items: []view.Section{
			{ID: "grants", Title: "Allowed on " + server, View: t},
			{ID: "policy", Title: "That server's team policy",
				View: view.Text{Body: strings.TrimPrefix(remoteSuppressedNote(server, gl.Suppressed), "\n\n")}},
		}}, nil
	}
	return t, nil
}

// remoteSuppressedNote is suppressedNote's remote cousin: the count comes
// from the server, and the ceiling's whereabouts are the server's to know —
// naming a local policy file here would point at the wrong machine.
func remoteSuppressedNote(server string, n int) string {
	return fmt.Sprintf("\n\n%s on %s %s suppressed by its team's policy.\n"+
		"%s not deleted: relaxing the policy on the server brings %s back.",
		format.Count(n, "grant", "grants"), server, format.Plural(n, "is", "are"),
		format.Plural(n, "It is", "They are"), format.Plural(n, "it", "them"))
}

// remoteAllow is `grant allow --server <name>`: prepare on the server, sign
// here, submit. The server builds the grant because its config, policy and
// catalogue are the ones that bind; the signature happens here because the
// key and the human are here — what gets signed is byte-for-byte what the
// server said it would store, which is the review step made structural.
func remoteAllow(ctx context.Context, req plugin.Request, server string) (view.View, error) {
	// Named here, and never filled in for you: the agents this machine knows
	// are this machine's, and the grant is for one on the server. Refused
	// locally rather than after the round trip, since the answer would be
	// the same and the passphrase would have been typed for nothing.
	if strings.TrimSpace(req.String("agent")) == "" {
		sf := req.Surface()
		return nil, view.Errorf("grant.noagent",
			"name the agent this is for on %s, with %s", server, sf.InputName("agent")).
			WithHint("`" + sf.Call("operator.status", plugin.Arg{Name: "server", Value: server}) +
				"` says what that server runs as")
	}
	spec := operatorid.IssueSpec{
		Target:  req.String("target"),
		Scope:   req.String("scope"),
		Profile: req.String("profile"),
		Agent:   req.String("agent"),
		TTL:     req.String("ttl"),
		Note:    req.String("note"),
		MaxUses: req.Int("max-uses"),
		Rate:    req.String("rate"),
	}
	// Refused here as well as by the server's builder, for the agent's
	// reason above: the answer is known before the passphrase is typed.
	if verr := givenRecord(req.Surface(), spec.Scope); verr != nil {
		return nil, verr
	}
	base, verr := operatorid.ServerURL(server)
	if verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: "would ask " + base + " to prepare this grant under its own policy, " +
			"sign the result with the operator key, and submit it — the passphrase is asked first"}, nil
	}
	pass, verr := operatorid.PromptSecret(req, false)
	if verr != nil {
		return nil, verr
	}
	signer, verr := operatorid.Unlock(pass)
	if verr != nil {
		return nil, verr
	}
	client := operatorid.Client{URL: base, Signer: signer}
	var prepared operatorid.Prepared
	if verr := client.Call(ctx, operatorid.VerbGrantPrepare, spec, &prepared); verr != nil {
		return nil, verr
	}
	// The review-before-signing step, for real: a compromised server that
	// can widen what comes back would otherwise turn this flow into a
	// signing oracle — the operator's key blessing authority nobody asked
	// for. Every spec-controlled field must round-trip, the times must be
	// sane by this machine's own clock, and the binding must name the
	// server actually dialed; the server's only licence is to clamp the
	// TTL downward.
	if verr := checkPrepared(req.Surface(), spec, base, prepared.Grant); verr != nil {
		return nil, verr
	}
	g := prepared.Grant
	core.SignWith(signer.GrantSigner(), &g)
	var issued core.Grant
	if verr := client.Call(ctx, operatorid.VerbGrantIssue, g, &issued); verr != nil {
		return nil, verr
	}
	msg := fmt.Sprintf("%s on %s may %s for %s (until %s)%s%s",
		subject(issued), server, describe(issued), format.Duration(issued.Expires.Sub(issued.Issued)),
		format.Clock(issued.Expires), usesSuffix(issued.MaxUses), rateSuffix(issued))
	// The server's own notes — a clamped TTL, an environment that is not
	// switched on — worded by the machine that knows.
	for _, n := range prepared.Notes {
		msg += "\n" + n
	}
	return view.Text{Body: msg}, nil
}

// checkPrepared holds the server's draft to what was asked. False economy
// to trust here and verify at submit: submit-side checks run on the same
// server that produced the draft, so the only verifier positioned against a
// hostile server is this one, on this machine, before the signature exists.
func checkPrepared(sf plugin.Surface, spec operatorid.IssueSpec, server string, g core.Grant) *view.Error {
	changed := func(field string, got, want any) *view.Error {
		return view.Errorf("core.operator.prepare.mismatch",
			"the server's draft changed %s to %v (asked: %v) — refusing to sign it", field, got, want)
	}
	if g.Sig != "" {
		return view.Errorf("core.operator.prepare.mismatch",
			"the server's draft arrived already signed — the signature is this machine's to make")
	}
	if g.Server != server {
		return changed("its server binding", g.Server, server)
	}
	if want := core.Normalize(spec.Target); g.Target != want {
		return changed("the target", g.Target, want)
	}
	// Byte for byte, as the builder takes it (givenRecord): a draft on the
	// record without the padding asked for is a grant on another record.
	if g.Scope != spec.Scope {
		return changed("the record scope", textclean.Record(g.Scope), textclean.Record(spec.Scope))
	}
	if want := strings.TrimSpace(spec.Profile); g.Profile != want {
		return changed("the profile", g.Profile, want)
	}
	if want := strings.TrimSpace(spec.Agent); g.Agent != want {
		return changed("the agent", g.Agent, want)
	}
	if g.Note != spec.Note {
		return changed("the note", g.Note, spec.Note)
	}
	if g.MaxUses != spec.MaxUses {
		return changed("the use limit", g.MaxUses, spec.MaxUses)
	}
	rateMax, rateWindow, verr := parseRate(sf, spec.Rate)
	if verr != nil {
		return verr
	}
	if g.RateMax != rateMax || g.RateWindow != rateWindow {
		return changed("the rate", fmt.Sprintf("%d/%s", g.RateMax, g.RateWindow), spec.Rate)
	}
	if g.TTL != strings.TrimSpace(spec.TTL) {
		return changed("the ttl string", g.TTL, strings.TrimSpace(spec.TTL))
	}
	if !strings.HasPrefix(g.From, core.FromOperatorPrefix) {
		return changed("the origin", g.From, core.FromOperatorPrefix+"<label>")
	}
	if g.Uses != 0 || len(g.Recent) != 0 {
		return view.Errorf("core.operator.prepare.mismatch",
			"the server's draft arrived pre-spent — refusing to sign it")
	}
	now := time.Now()
	if d := now.Sub(g.Issued); d < -2*time.Minute || d > 2*time.Minute {
		return view.Errorf("core.operator.prepare.mismatch",
			"the server's draft is timestamped %s from this machine's clock — check both clocks",
			d.Round(time.Second))
	}
	asked := core.DefaultTTL
	if raw := strings.TrimSpace(spec.TTL); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			return view.Errorf("grant.badttl", "%q is not a duration", raw)
		}
		asked = parsed
	}
	// Downward is the server's licence — its policy may be tighter than the
	// ask — and only downward.
	if window := g.Expires.Sub(g.Issued); window <= 0 || window > asked || window > core.MaxTTL {
		return changed("the lifetime", g.Expires.Sub(g.Issued), asked)
	}
	return nil
}

// remoteRevoke is `grant revoke --server <name>`. A dry run still crosses
// the network with write off — see operator.RevokeSpec.DryRun — so the
// preview is the server's truth, not this machine's guess.
func remoteRevoke(ctx context.Context, req plugin.Request, server string, spec operatorid.RevokeSpec) (view.View, error) {
	base, verr := operatorid.ServerURL(server)
	if verr != nil {
		return nil, verr
	}
	pass, verr := operatorid.PromptSecret(req, false)
	if verr != nil {
		return nil, verr
	}
	signer, verr := operatorid.Unlock(pass)
	if verr != nil {
		return nil, verr
	}
	client := operatorid.Client{URL: base, Signer: signer}
	// An exact revoke asks first, with write off, whether the server knows
	// the switch. One older than it ignores the field and reads every
	// selector left out as every grant: the x on a roster row, meant for
	// the grant naming no record, would take back each grant on its target
	// and agent. Its answer does not say Exact, and nothing is taken back.
	if spec.Exact {
		probe := spec
		probe.DryRun = true
		var seen operatorid.RevokeOutcome
		if verr := client.Call(ctx, operatorid.VerbGrantRevoke, probe, &seen); verr != nil {
			return nil, verr
		}
		if !seen.Exact {
			sf := req.Surface()
			return nil, view.Errorf("grant.remote.exact",
				"%s does not know %s, and would take back every grant the other selectors match", server,
				sf.InputName("exact")).
				WithHint("`" + sf.Call("operator.status", plugin.Arg{Name: "server", Value: server}) +
					"` says which rta it runs; `" + sf.Call("grant.list", plugin.Arg{Name: "server", Value: server}) +
					"` shows what a revoke without it would match")
		}
		if spec.DryRun {
			return view.Text{Body: revokeBody(req.Surface(), spec, server, seen, nil, true)}, nil
		}
	}
	var out operatorid.RevokeOutcome
	if verr := client.Call(ctx, operatorid.VerbGrantRevoke, spec, &out); verr != nil {
		return nil, verr
	}
	return view.Text{Body: revokeBody(req.Surface(), spec, server, out, nil, req.DryRun)}, nil
}

// filterWords says what a roster was narrowed to: "the role dev", "the agent
// claude", or both.
func filterWords(role, agent string) string {
	var parts []string
	if role != "" {
		parts = append(parts, "the role "+role)
	}
	if agent != "" {
		parts = append(parts, "the agent "+agent)
	}
	return strings.Join(parts, " and ")
}
