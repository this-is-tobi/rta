package pluginhost

import (
	"context"
	"errors"

	"github.com/this-is-tobi/rta/internal/tlstrust"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// trusting runs a call to capability id, whose declared inputs say which
// server it reaches, with what the operator trusted for that server
// (internal/tlstrust), and remembers what the server presented when its
// certificate was refused.
//
// **Here, at the one door every surface's call goes through**, rather than in
// each of them: the CLI, the TUI's runs and tiles and the MCP bridge all reach
// a plugin through this, and a surface that forgot the trust would refuse a
// server the others reach.
//
// Through a `kube:` forward the first certificate is trusted as it arrives,
// and the call is made again once with it — the handshake failed before the
// call sent anything, so a write is no more done twice than a read. Anywhere
// else, and for a certificate other than the one trusted, it waits for the
// operator, and the refusal says how they look at it.
func (c *Client) trusting(ctx context.Context, id string, inputs []plugin.Field, req plugin.Request) (view.View, error) {
	ns, server := plugin.Namespace(id), tlstrust.ServerOf(inputs, req)
	if server == "" {
		return c.call(ctx, id, req)
	}
	// A store that does not verify is honoured in nothing: the call runs with
	// nothing trusted, as on a machine that never trusted anything, and is told
	// why only if that is what refuses it.
	trusted, unread := tlstrust.Trusted(ns, server)
	v, err := c.call(ctx, id, req.WithTrusted(trusted))
	// At most twice round: a certificate trusted as it arrived leaves trusted
	// set, so a refusal on the call made again with it is kept, never trusted.
	for {
		refused := presenting(err)
		if refused == nil {
			return v, err
		}
		// What the server presented was for the host to read, and the host has.
		told := refused.WithPresented(nil)
		if unread != nil {
			return v, unkept(told, unread, req.Surface())
		}
		auto := trusted == nil && req.Tunnel() == plugin.TunnelKube
		outcome, rec, verr := tlstrust.Record(ns, server, req.Profile(), refused.Presented, auto)
		switch {
		case verr != nil:
			return v, unkept(told, verr, req.Surface())
		case outcome == tlstrust.TrustedNow:
			trusted = rec.Trusted.Chain
			tlstrust.Note(ctx, "trusted the certificate "+server+" presented, as it arrived through the kube: forward"+
				", sha256 "+rec.Trusted.Fingerprint())
			v, err = c.call(ctx, id, req.WithTrusted(trusted))
			continue
		case outcome == tlstrust.Waiting:
			tlstrust.Note(ctx, "the certificate "+server+" presented, sha256 "+rec.Pending.Fingerprint()+
				", waits for `rta cert trust "+server+"`")
			return v, waiting(told, rec, req.Surface())
		}
		return v, told
	}
}

// withTrust is req carrying what is trusted for the server it reaches, for the
// reads a form and a completion make: what is trusted already and nothing
// more, since a keystroke remembers nothing and a form is not a call.
func withTrust(id string, inputs []plugin.Field, req plugin.Request) plugin.Request {
	server := tlstrust.ServerOf(inputs, req)
	if server == "" {
		return req
	}
	trusted, verr := tlstrust.Trusted(plugin.Namespace(id), server)
	if verr != nil {
		return req
	}
	return req.WithTrusted(trusted)
}

// presenting is err as the refusal of a certificate that carries what the
// server presented (view.Error.Presented), nil for any other answer.
func presenting(err error) *view.Error {
	var verr *view.Error
	if errors.As(err, &verr) && verr != nil && len(verr.Presented) > 0 {
		return verr
	}
	return nil
}

// waiting is the refusal of a certificate kept for the operator, saying how
// they look at it: at a terminal the command that shows it and asks, and to
// an agent the operator's to run, naming no server, since through a forward
// that is a coordinate an agent is never told.
func waiting(refused *view.Error, rec tlstrust.Server, sf plugin.Surface) *view.Error {
	shows := "shows the certificate it presented and trusts it"
	if rec.Trusted != nil {
		shows = "shows what it presents now beside it, and trusts it"
	}
	next := "`" + sf.Call("cert.trust", plugin.Arg{Name: "server", Value: rec.Server, Positional: true}) + "` " +
		shows + " on your word"
	if sf == plugin.SurfaceMCP {
		next = plugin.AskOperator("cert trust") + ", which " + shows + " on their word"
	}
	if rec.Trusted == nil {
		return refused.WithHint(refused.Hint + " — or, if it is the server meant, " + next)
	}
	how := "as it arrived through a forward"
	if rec.Trusted.How == tlstrust.ByOperator {
		how = "at a terminal"
	}
	return refused.WithHint(refused.Hint + "; the one it replaces was trusted " + rec.Trusted.At.Format("2006-01-02") +
		" " + how + " — " + next)
}

// unkept is a refusal of a certificate rta could not keep for the operator to
// look at, because what is trusted could not be read or written: at a
// terminal why, and to an agent that it is the operator's.
func unkept(refused, store *view.Error, sf plugin.Surface) *view.Error {
	if sf == plugin.SurfaceMCP {
		return refused.WithHint(refused.Hint + "; rta could not keep the certificate it presented for the operator " +
			"to trust (" + store.Code + ")")
	}
	return refused.WithHint(refused.Hint + "; rta could not keep the certificate it presented to be trusted: " +
		store.Message + " — " + store.Hint)
}
