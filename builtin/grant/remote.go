package grant

import (
	"fmt"
	"strings"

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

// remoteRevoke is `grant revoke --server <name>`. A dry run still crosses
// the network with write off — see operator.RevokeSpec.DryRun — so the
// preview is the server's truth, not this machine's guess.
func remoteRevoke(req plugin.Request, server string) (view.View, error) {
	spec := operatorid.RevokeSpec{
		All:     req.Bool("all"),
		Target:  req.String("target"),
		Scope:   req.String("scope"),
		Profile: req.String("profile"),
		Agent:   req.String("agent"),
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
