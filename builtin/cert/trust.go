package cert

import (
	"context"
	"crypto/x509"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/internal/tlstrust"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// This file is the person's half of trusting a server on first use
// (internal/tlstrust): looking at what a server presented, trusting it, and
// taking it back. None of it is a tool: which certificate stands in for a CA
// on a connection decides where a plugin sends its credential, and that is the
// one question an agent — or a prompt it read — must never answer.

// trustCapabilities are the three capabilities over what is trusted.
func trustCapabilities() []plugin.Capability {
	serverHelp := "the server as a refusal names it: host:port, or a forward's coordinate"
	pluginField := plugin.Field{Name: "plugin", Type: plugin.String, Suggest: suggestPlugins,
		Help: "only what this plugin met, when two reach the same server"}
	return []plugin.Capability{
		{
			ID:      "cert.trust",
			Flash:   true,
			Summary: "Trust the certificate a server presented, after looking at it",
			Description: "A server whose certificate nothing on this machine vouches for — a self-signed " +
				"appliance, a cluster's own CA, a certificate that expired — is refused on its first call, " +
				"and what it presented is kept here. This shows it, the SHA-256 `cert.inspect` prints with " +
				"it, and on your yes trusts it for that plugin's calls to that server: a later call reaches " +
				"it, and a renewal signed by the CA it came with too, while a server presenting anything else " +
				"is refused again and its certificate kept for you to look at. Through a `kube:` forward the " +
				"first certificate is trusted as it arrives, and only a change waits here. With no server, " +
				"every certificate waiting is shown, one at a time. Never reachable over MCP.",
			Safety:     plugin.Write,
			Idempotent: true,
			Inputs: []plugin.Field{
				{Name: "server", Type: plugin.String, Positional: true, Suggest: suggestWaiting, Help: serverHelp},
				pluginField,
			},
			HumanOnly: true,
			Run:       runTrust,
		},
		{
			ID:      "cert.trusted",
			Summary: "The servers whose certificates this machine trusts, and those waiting",
			Description: "Every server a plugin's calls met with a certificate nothing here vouches for: the one " +
				"trusted for it, when, and how — as it arrived through a `kube:` forward, or on your word — and " +
				"the one waiting for you to look at, the first a server presented or one other than the one " +
				"trusted. Never reachable over MCP: which forwards and servers the operator reaches is theirs.",
			Safety:     plugin.Read,
			Idempotent: true,
			NoPreview:  true,
			HumanOnly:  true,
			// Trusting and forgetting are row actions on the list itself, where both
			// halves of what they act on are on the row, matched to their inputs by
			// column name.
			Actions: []plugin.Action{
				{Key: "t", Label: "trust", Target: "cert.trust", Source: plugin.ActionRow},
				{Key: "x", Label: "forget", Target: "cert.forget", Source: plugin.ActionRow},
			},
			Run: runTrusted,
		},
		{
			ID:      "cert.forget",
			Flash:   true,
			Summary: "Forget what was trusted for a server",
			Description: "Takes back the certificate trusted for a server and any waiting: the next call there " +
				"meets its certificate as a first one, so a server behind a forward is trusted again as it " +
				"arrives, and any other waits for you. For a connection that now names a CA, a server gone, or " +
				"a certificate trusted by mistake. Never reachable over MCP.",
			Safety:     plugin.Write,
			Idempotent: true,
			Inputs: []plugin.Field{
				{Name: "server", Type: plugin.String, Positional: true, Required: true, Suggest: suggestRemembered,
					Help: serverHelp},
				pluginField,
			},
			HumanOnly: true,
			Run:       runForget,
		},
	}
}

// chosen is the records a call names: every one, or those of server, narrowed
// to one plugin when it names one.
func chosen(req plugin.Request, keep func(tlstrust.Server) bool) ([]tlstrust.Server, *view.Error) {
	servers, verr := tlstrust.List()
	if verr != nil {
		return nil, verr
	}
	server, ns := strings.TrimSpace(req.String("server")), strings.TrimSpace(req.String("plugin"))
	var out []tlstrust.Server
	for _, s := range servers {
		if (server == "" || s.Server == server) && (ns == "" || s.Plugin == ns) && keep(s) {
			out = append(out, s)
		}
	}
	return out, nil
}

func waitingOnly(s tlstrust.Server) bool { return s.Pending != nil }

func runTrust(_ context.Context, req plugin.Request) (view.View, error) {
	sf := req.Surface()
	named := strings.TrimSpace(req.String("server")) != ""
	waiting, verr := chosen(req, waitingOnly)
	if verr != nil {
		return nil, verr
	}
	if len(waiting) == 0 {
		what := "no certificate waits to be trusted"
		if named {
			what = "no certificate " + req.String("server") + " presented waits to be trusted"
		}
		return nil, view.Errorf("core.trust.none", "%s", what).
			WithHint("a call that meets a certificate nothing here vouches for keeps it here; " +
				sf.CapabilityName("cert.trusted") + " lists what is kept").WithInput("server")
	}
	// Every one waiting, unseen, is not a thing a script may trust: at a
	// terminal each is asked about, and anywhere else the server is named.
	if !named && !req.CanConfirm() && !req.DryRun {
		return nil, view.Errorf("core.trust.unnamed", "%d certificates wait, and none is named", len(waiting)).
			WithHint("name the server to trust what it presented — " + sf.CapabilityName("cert.trusted") +
				" lists them — or run this at a terminal, which shows each and asks").WithInput("server")
	}
	if req.DryRun {
		cards := make([]string, len(waiting))
		for i, s := range waiting {
			cards[i] = "would trust " + card(s)
		}
		return view.Text{Body: strings.Join(cards, "\n\n")}, nil
	}
	table := view.Table{Columns: []view.Column{{Name: "Plugin"}, {Name: "Server"}, {Name: "Trusted"}, {Name: "SHA-256"}}}
	declined := 0
	for _, s := range waiting {
		if !req.Yes && req.CanConfirm() && !req.Confirm(card(s)+"\n\nTrust it for "+s.Plugin+"'s calls to "+s.Server+"?") {
			declined++
			table.Add(view.Plain(s.Plugin), view.Plain(s.Server), view.Plain("no"), view.Plain(short(s.Pending.Fingerprint())))
			continue
		}
		rec, verr := tlstrust.Trust(s.Plugin, s.Server, s.Pending.Fingerprint())
		if verr != nil {
			return nil, verr
		}
		table.Add(view.Plain(rec.Plugin), view.Plain(rec.Server), view.Plain("yes"), view.Plain(short(rec.Trusted.Fingerprint())))
	}
	if declined == len(waiting) {
		// The code the destructive prompt ends with, so a "no" exits 3 and stops
		// a `&&` chain as that one does (see builtin/grant's notConfirmed).
		return nil, &view.Error{Code: "core.confirm.required", Message: "not confirmed — nothing was trusted",
			Hint: "the certificate stays kept, and the server refused, until it is trusted or forgotten"}
	}
	return table, nil
}

func runTrusted(_ context.Context, req plugin.Request) (view.View, error) {
	servers, verr := chosen(req, func(tlstrust.Server) bool { return true })
	if verr != nil {
		return nil, verr
	}
	table := view.Table{
		// Plugin and Server are spelled as cert.trust's and cert.forget's inputs
		// are, so a row action acts on the record under the cursor.
		Columns: []view.Column{{Name: "Plugin"}, {Name: "Server"}, {Name: "State", Kind: view.KindStatus},
			{Name: "Since", Kind: view.KindTimestamp}, {Name: "How", Priority: 2}, {Name: "SHA-256", Priority: 1},
			{Name: "Certificate", Priority: 3}, {Name: "Ends", Kind: view.KindTimestamp, Priority: 2}},
		Empty: "no server's certificate is trusted or waiting",
	}
	at := time.Now()
	for _, s := range servers {
		if s.Trusted != nil {
			state := "OK trusted"
			if s.Pending != nil {
				state = "WARN changed"
			}
			table.Add(rowOf(s.Plugin, s.Server, state, *s.Trusted, at)...)
		}
		if s.Pending != nil && s.Trusted == nil {
			table.Add(rowOf(s.Plugin, s.Server, "WARN waiting", *s.Pending, at)...)
		}
	}
	for _, s := range servers {
		if s.Trusted != nil && s.Pending != nil {
			table.Warnings = append(table.Warnings, view.Error{Code: "core.trust.changed", Advisory: true,
				Message: s.Server + " presented " + s.Plugin + " a certificate other than the one trusted, on " +
					s.Pending.At.Local().Format("2006-01-02") + ", and its calls are refused",
				Hint: "`" + req.Surface().Call("cert.trust", plugin.Arg{Name: "server", Value: s.Server, Positional: true}) +
					"` shows it beside the one it replaces"})
		}
	}
	return table, nil
}

// rowOf is one record's row, as of at.
func rowOf(ns, server, state string, seen tlstrust.Seen, at time.Time) []view.Cell {
	how := "seen, not trusted"
	switch seen.How {
	case tlstrust.ByForward:
		how = "as it arrived through a forward"
	case tlstrust.ByOperator:
		how = "on your word"
	}
	if seen.Profile != "" {
		how += ", profile " + seen.Profile
	}
	subject, ends := "", view.Plain("")
	if leaf := seen.Leaf(); leaf != nil {
		subject = leaf.Subject.String()
		text := leaf.NotAfter.Local().Format("2006-01-02")
		if leaf.NotAfter.Before(at) {
			text += " (ended)"
		}
		ends = view.Date(text, leaf.NotAfter.Local())
	}
	return []view.Cell{view.Plain(ns), view.Plain(server), view.Plain(state),
		view.Time(seen.At.Local().Format("2006-01-02 15:04"), seen.At), view.Plain(how),
		view.Plain(short(seen.Fingerprint())), view.Plain(subject), ends}
}

func runForget(_ context.Context, req plugin.Request) (view.View, error) {
	server, ns := strings.TrimSpace(req.String("server")), strings.TrimSpace(req.String("plugin"))
	whose := "every plugin"
	if ns != "" {
		whose = ns
	}
	if req.DryRun {
		return view.Text{Body: "would forget what " + whose + " trusted or kept for " + server +
			": its next call there meets the certificate as a first one"}, nil
	}
	gone, verr := tlstrust.Forget(ns, server)
	if verr != nil {
		return nil, verr
	}
	if gone == 0 {
		return nil, view.Errorf("core.trust.none", "nothing is trusted or kept for %s", server).
			WithHint(req.Surface().CapabilityName("cert.trusted") + " lists what is").WithInput("server")
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "forgot", Value: server + " for " + whose},
		{Key: "effect", Value: "its next call there meets the certificate as a first one: through a kube: " +
			"forward trusted as it arrives, anywhere else kept for you to look at"},
	}}, nil
}

// card is what a person reads before trusting what s presented: the
// certificate as `cert.inspect` would describe it, its SHA-256 whole, and the
// one it replaces.
func card(s tlstrust.Server) string {
	p := *s.Pending
	lines := []string{fmt.Sprintf("%s presented to %s%s, on %s:", s.Server, s.Plugin, through(p.Profile),
		p.At.Local().Format("2006-01-02 15:04"))}
	lines = append(lines, described(p)...)
	if s.Trusted != nil {
		lines = append(lines, "", fmt.Sprintf("in place of the one trusted %s %s:", s.Trusted.At.Local().Format("2006-01-02"),
			howWord(s.Trusted.How)))
		lines = append(lines, described(*s.Trusted)...)
	}
	return strings.Join(lines, "\n")
}

func through(profile string) string {
	if profile == "" {
		return ""
	}
	return " through profile " + profile
}

func howWord(h tlstrust.How) string {
	if h == tlstrust.ByForward {
		return "as it arrived through a forward"
	}
	return "on your word"
}

// described is a chain's leaf as a card lists it.
func described(seen tlstrust.Seen) []string {
	leaf := seen.Leaf()
	if leaf == nil {
		return []string{"  sha256   " + seen.Fingerprint()}
	}
	issuer := leaf.Issuer.String()
	if issuer == leaf.Subject.String() {
		issuer += " (self-signed)"
	}
	valid := leaf.NotBefore.Local().Format("2006-01-02") + " to " + leaf.NotAfter.Local().Format("2006-01-02")
	if leaf.NotAfter.Before(time.Now()) {
		valid += " — ended"
	}
	out := []string{"  subject  " + leaf.Subject.String(), "  issuer   " + issuer, "  valid    " + valid}
	if names := namesOf(leaf); names != "" {
		out = append(out, "  names    "+names)
	}
	if len(seen.Chain) > 1 {
		out = append(out, fmt.Sprintf("  chain    %d certificates, a renewal signed by a CA among them passes too",
			len(seen.Chain)))
	}
	return append(out, "  sha256   "+seen.Fingerprint())
}

func namesOf(c *x509.Certificate) string {
	names := slices.Clone(c.DNSNames)
	for _, ip := range c.IPAddresses {
		names = append(names, ip.String())
	}
	return strings.Join(names, ", ")
}

// short is a fingerprint as a table lists it: enough to tell two apart at a
// glance, the whole of it being on the card.
func short(fingerprint string) string {
	if len(fingerprint) <= 16 {
		return fingerprint
	}
	return fingerprint[:16] + "…"
}

// suggestWaiting completes the servers whose certificates wait.
func suggestWaiting(_ context.Context, req plugin.Request) []string {
	return serversWhere(req, waitingOnly)
}

// suggestRemembered completes every server something is remembered for.
func suggestRemembered(_ context.Context, req plugin.Request) []string {
	return serversWhere(req, func(tlstrust.Server) bool { return true })
}

func serversWhere(req plugin.Request, keep func(tlstrust.Server) bool) []string {
	servers, verr := tlstrust.List()
	if verr != nil {
		return nil
	}
	ns := strings.TrimSpace(req.String("plugin"))
	var out []string
	for _, s := range servers {
		if keep(s) && (ns == "" || s.Plugin == ns) && !slices.Contains(out, s.Server) {
			out = append(out, s.Server)
		}
	}
	return out
}

// suggestPlugins completes the plugins something is remembered for, for the
// server already typed when there is one.
func suggestPlugins(_ context.Context, req plugin.Request) []string {
	servers, verr := tlstrust.List()
	if verr != nil {
		return nil
	}
	server := strings.TrimSpace(req.String("server"))
	var out []string
	for _, s := range servers {
		if (server == "" || s.Server == server) && !slices.Contains(out, s.Plugin) {
			out = append(out, s.Plugin)
		}
	}
	return out
}
