package grant

import (
	"fmt"
	"strings"
	"time"

	core "github.com/this-is-tobi/rule-them-all/internal/grant"
	operatorid "github.com/this-is-tobi/rule-them-all/internal/operator"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// remoteList is `grant list --server <name>`: the same roster question asked
// of a remote rta server, as a signed operator call. The rows render through
// grantsTable exactly as local ones do — the operator is deciding the same
// things about them — but everything judged against local state stays out:
// staleness is the server's config's business, and the suppressed count and
// empty-case hints arrive from the server's own store rather than being
// recomputed against files that describe this machine.
func remoteList(req plugin.Request, server string) (view.View, error) {
	if req.Bool("detail") {
		return nil, view.Errorf("grant.remote.detail",
			"--detail describes this machine's catalogue, not %s's", server).
			WithHint("run `rta grant list --server " + server + "` without --detail; " +
				"the reach tiers depend on flags the server was started with")
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
	if verr := (operatorid.Client{URL: base, Signer: signer}).Call(operatorid.VerbGrantList, nil, &gl); verr != nil {
		return nil, verr
	}
	if len(gl.Grants) == 0 {
		body := fmt.Sprintf("No active grants on %s — its agents can only read.", server)
		if gl.Suppressed > 0 {
			body += remoteSuppressedNote(server, gl.Suppressed)
		}
		return view.Text{Body: body}, nil
	}
	t := grantsTable(gl.Grants, nil)
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
	return fmt.Sprintf("\n\n%d grant(s) on %s are suppressed by its team's policy.\n"+
		"They are not deleted: relaxing the policy on the server brings them back.", n, server)
}

// remoteAllow is `grant allow --server <name>`: prepare on the server, sign
// here, submit. The server builds the grant because its config, policy and
// catalogue are the ones that bind; the signature happens here because the
// key and the human are here — what gets signed is byte-for-byte what the
// server said it would store, which is the review step made structural.
func remoteAllow(req plugin.Request, server string) (view.View, error) {
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
	if verr := client.Call(operatorid.VerbGrantPrepare, spec, &prepared); verr != nil {
		return nil, verr
	}
	// The review-before-signing step, for real: a compromised server that
	// can widen what comes back would otherwise turn this flow into a
	// signing oracle — the operator's key blessing authority nobody asked
	// for. Every spec-controlled field must round-trip, the times must be
	// sane by this machine's own clock, and the binding must name the
	// server actually dialed; the server's only licence is to clamp the
	// TTL downward.
	if verr := checkPrepared(spec, base, prepared.Grant); verr != nil {
		return nil, verr
	}
	g := prepared.Grant
	core.SignWith(signer.GrantSigner(), &g)
	var issued core.Grant
	if verr := client.Call(operatorid.VerbGrantIssue, g, &issued); verr != nil {
		return nil, verr
	}
	msg := fmt.Sprintf("agents on %s may %s for %s (until %s)%s%s",
		server, describe(issued), issued.Expires.Sub(issued.Issued),
		issued.Expires.Format("15:04:05"), usesSuffix(issued.MaxUses), rateSuffix(issued))
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
func checkPrepared(spec operatorid.IssueSpec, server string, g core.Grant) *view.Error {
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
	if want := strings.TrimSpace(spec.Scope); g.Scope != want {
		return changed("the record scope", g.Scope, want)
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
		return changed("--max-uses", g.MaxUses, spec.MaxUses)
	}
	rateMax, rateWindow, verr := parseRate(spec.Rate)
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
func remoteRevoke(req plugin.Request, server string) (view.View, error) {
	spec := operatorid.RevokeSpec{
		All:     req.Bool("all"),
		Target:  strings.TrimSpace(req.String("target")),
		Scope:   strings.TrimSpace(req.String("scope")),
		Profile: strings.TrimSpace(req.String("profile")),
		Agent:   strings.TrimSpace(req.String("agent")),
		DryRun:  req.DryRun,
	}
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
	var out operatorid.RevokeOutcome
	if verr := (operatorid.Client{URL: base, Signer: signer}).Call(operatorid.VerbGrantRevoke, spec, &out); verr != nil {
		return nil, verr
	}
	return view.Text{Body: revokeBody(spec.Target, out, req.DryRun)}, nil
}
