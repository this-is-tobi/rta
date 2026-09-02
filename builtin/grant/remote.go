package grant

import (
	"fmt"
	"strings"

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
