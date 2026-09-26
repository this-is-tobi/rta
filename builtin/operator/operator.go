// Package operator is the person's side of the remote operator channel: the
// key that proves them to a remote server, and the glance at what a server
// says about itself.
//
// None of it is reachable over MCP, for builtin/agent's reason verbatim: the
// channel exists so a human can manage servers an agent talks through, and
// an agent that could mint or introspect the operator identity would be
// holding the one credential the design keeps out of its reach. The refusal
// is belt beside the real wall — an agent cannot sign without the
// passphrase whatever surface it asks on — but "not here" stays the right
// first answer.
package operator

import (
	"context"
	"fmt"

	id "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Plugin returns the operator plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "operator",
		Summary: "Your identity for managing remote rta servers: a key only your passphrase can use",
		Capabilities: []plugin.Capability{
			{
				ID:      "operator.init",
				Summary: "Mint your operator key — the identity remote servers enroll",
				Description: "Generates an ed25519 keypair: the private half stays on this machine, " +
					"encrypted under a passphrase that is never stored, never accepted from the " +
					"environment, and refused on the command line; the public half is what you paste " +
					"into a server's roster (`rta mcp serve --operators`). Signing anything takes the passphrase, so an " +
					"agent that reads every file on this machine still cannot speak as you. Refuses " +
					"to overwrite an existing key: rotating is `rm` plus re-enrolling everywhere, " +
					"deliberately manual.",
				Safety:     plugin.Write,
				Idempotent: false,
				Inputs: []plugin.Field{
					id.PassphraseField,
					{Name: "label", Type: plugin.String, Default: "operator",
						Help: "how server rosters and audit lines will name you (e.g. your handle)"},
				},
				HumanOnly: true,
				Run:       runInit,
			},
			{
				ID:      "operator.status",
				Summary: "Your operator key at a glance — or a remote server's, given one",
				Description: "Without `server`: whether this machine holds an operator key, its " +
					"fingerprint, and the exact line to paste into a server's roster (`rta mcp serve " +
					"--operators`). With `server` (a name from remotes.yaml, beside your config): asks that server " +
					"who it is — version, agent name, guard state, enrolled operators — as a signed " +
					"call, so it also proves your enrollment end to end.",
				Safety:     plugin.Read,
				Idempotent: true,
				NoPreview:  true, // an enrollment page for setup moments, not a dashboard tile
				Inputs: []plugin.Field{
					{Name: "label", Type: plugin.String, Default: "operator",
						Help: "the label to print in the roster line"},
					{Name: "server", Type: plugin.String, Local: true, Remote: true,
						Help: "ask this server from remotes.yaml instead of describing the local key"},
					id.PassphraseField.OnlyWith("server"),
				},
				HumanOnly: true,
				Run:       runStatus,
			},
		},
	}
}

func runInit(_ context.Context, req plugin.Request) (view.View, error) {
	label := req.String("label")
	if verr := id.CheckLabel(label); verr != nil {
		return nil, verr
	}
	if id.Exists() {
		return nil, view.Errorf("core.operator.exists", "an operator key already exists").
			WithHint(req.Surface().CapabilityName("operator.status") + " shows it; `rm " + id.Path() + "` first if you mean to rotate, " +
				"then re-enroll the new key on every server")
	}
	if req.DryRun {
		return view.Text{Body: "would mint an operator keypair at " + id.Path() +
			", encrypted under a passphrase asked at the prompt — nothing leaves this machine"}, nil
	}
	pass, verr := id.PromptSecret(req, true)
	if verr != nil {
		return nil, verr
	}
	signer, verr := id.Init(pass)
	if verr != nil {
		return nil, verr
	}
	line, verr := id.RosterLine(label)
	if verr != nil {
		return nil, verr
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "operator key", Value: "minted — " + id.Path()},
		{Key: "fingerprint", Value: signer.Fingerprint()},
		{Key: "enroll", Value: enrollLine + line},
		{Key: "forgotten?", Value: "rm " + id.Path() + " and init again — every roster then needs the new line"},
	}}, nil
}

// enrollLine introduces the roster line an operator hands to a server. The
// roster is the file the server is started with, and `rta mcp serve
// --operators` is the only spelling that has: serving is a command, never a
// capability another surface could name its own way.
const enrollLine = "add this line to the roster a server is started with (`rta mcp serve --operators`):\n"

func runStatus(ctx context.Context, req plugin.Request) (view.View, error) {
	if server := req.String("server"); server != "" {
		return remoteStatus(ctx, req, server)
	}
	if !id.Exists() {
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "operator key", Value: "none — this machine cannot manage remote servers yet"},
			{Key: "mint one", Value: req.Surface().Call("operator.init")},
		}}, nil
	}
	label := req.String("label")
	if verr := id.CheckLabel(label); verr != nil {
		return nil, verr
	}
	line, verr := id.RosterLine(label)
	if verr != nil {
		return nil, verr
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "operator key", Value: id.Path()},
		{Key: "fingerprint", Value: id.Fingerprint()},
		{Key: "since", Value: id.Created().Local().Format("2006-01-02 15:04")},
		{Key: "enroll", Value: enrollLine + line},
	}}, nil
}

func remoteStatus(ctx context.Context, req plugin.Request, server string) (view.View, error) {
	base, verr := id.ServerURL(server)
	if verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: "would ask " + base + " for its status, as a signed operator call " +
			"— the passphrase is asked first"}, nil
	}
	pass, verr := id.PromptSecret(req, false)
	if verr != nil {
		return nil, verr
	}
	signer, verr := id.Unlock(pass)
	if verr != nil {
		return nil, verr
	}
	var st id.Status
	if verr := (id.Client{URL: base, Signer: signer}).Call(ctx, id.VerbStatus, nil, &st); verr != nil {
		return nil, verr
	}
	guardCell := "off — anything that can run commands on that machine can issue a grant there"
	if st.GuardOn {
		guardCell = "on"
		if st.GuardFingerprint != "" {
			guardCell += " — key " + st.GuardFingerprint
		}
	}
	agent := st.Agent
	if agent == "" {
		agent = "—"
	}
	enrolled := make([]string, 0, len(st.Operators))
	for _, o := range st.Operators {
		enrolled = append(enrolled, o.String())
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "server", Value: server + " (" + base + ")"},
		{Key: "version", Value: st.Version},
		{Key: "agent", Value: agent},
		{Key: "guard", Value: guardCell},
		{Key: "operators", Value: fmt.Sprintf("%d enrolled: %s", len(st.Operators),
			joinOr(enrolled, "none"))},
	}}, nil
}

func joinOr(items []string, empty string) string {
	if len(items) == 0 {
		return empty
	}
	out := items[0]
	for _, s := range items[1:] {
		out += ", " + s
	}
	return out
}
