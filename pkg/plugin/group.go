package plugin

import (
	"fmt"
	"slices"
	"strings"
)

// maxGroup bounds a group name. A group is drawn as one row, a rule with the name
// in it, in a form that may be eighty columns wide: the conventional names are
// under ten characters, and a name that needs more than this is a description
// trying to be a heading.
const maxGroup = 24

// checkGroup holds a group name to being a heading: short, lowercase, printable.
//
// Printable ASCII and not "any text" because a group is drawn in a rule beside
// the box it heads and printed on `rta explain`, where a character that is not one
// the terminal draws in one cell, or not at all, breaks the row. Lowercase because
// the headings of a form are a vocabulary and `network` and `Network` would be two
// blocks that read as one. No space at either end because the rule puts its own.
func checkGroup(capability, input, group string) error {
	if group == "" {
		return nil
	}
	for _, r := range group {
		if r < 0x20 || r > 0x7e {
			return fmt.Errorf("capability %q: input %q has group %q, which holds %q; a group is "+
				"printable ASCII, drawn as a heading on every surface", capability, input, group, r)
		}
		if r >= 'A' && r <= 'Z' {
			return fmt.Errorf("capability %q: input %q has group %q; a group is lowercase, so "+
				"that two spellings of one name are not two blocks", capability, input, group)
		}
	}
	if n := len(group); n > maxGroup {
		return fmt.Errorf("capability %q: input %q has group %q, %d characters, want at most %d; "+
			"a group is a heading drawn as one row", capability, input, group, n, maxGroup)
	}
	if strings.TrimSpace(group) != group {
		return fmt.Errorf("capability %q: input %q has group %q with a space at its start or end; "+
			"the heading puts its own", capability, input, group)
	}
	return nil
}

// Connection reports whether f is part of the connection c is aimed at: an input
// only the person at the terminal may give (Local) that an operator's profile can
// fill (ProfileFillable) — a host, a port, how to verify the server, and the
// credential the environment supplies, which no config states but is as much the
// connection as the host beside it — left among the call's own inputs, pg.status
// opened on its password with every other box headed under it.
//
// A capability whose config states no such setting has no connection to speak of:
// the passphrase of a store is Local and fillable too, and is not the connection
// of anything. An input with a default from the config that an agent may also give
// (a limit, a prefix) is the call's own.
//
// Derived from what the author already declared, like ProfileFillable, so nothing
// here is something a declaration can claim for itself.
func Connection(c Capability, f Field) bool {
	if !f.Local || !ProfileFillable(c, f) {
		return false
	}
	return slices.ContainsFunc(c.Inputs, func(o Field) bool { return o.Local && o.Config != "" })
}

// Group names the block f is shown in on a form and on `rta explain`, or "" when
// it is in none.
//
// **A group is a heading and nothing more.** It changes nothing about who may
// supply the input, what a profile fences or what an agent is offered, and it is
// not read by anything that decides. The author declares it on the input
// (Field.Group); the names a connection uses by convention are network, tls,
// credentials and target.
//
// A connection input that declares none gets one from its name and its type, so
// that a plugin released before the field existed reads as grouped on the first
// release of rta that carries it. The fallback is this function and nowhere else,
// so every surface agrees and an author can read what they would get:
//
//   - a Secret or SecretSlice, or a name among user, username, password, token,
//     access-key, secret-key, client-id and client-secret: credentials;
//   - a name holding tls, ssl, ca-file, cert or key-file: tls;
//   - a name among address, host, port, url, socket, endpoint, context and
//     region: network;
//   - anything else: target, what is selected inside the service.
//
// The first match wins, in that order, so a masked box is a credential whatever
// it is called. A call's own input is in a group only when it says so: the
// fallback names the blocks of a connection and does not head a call's inputs by
// what they happen to be called.
func Group(c Capability, f Field) string {
	if f.Group != "" {
		return f.Group
	}
	if !Connection(c, f) {
		return ""
	}
	return connectionGroup(f)
}

var (
	credentialNames = []string{"user", "username", "password", "token", "access-key", "secret-key", "client-id", "client-secret"}
	networkNames    = []string{"address", "host", "port", "url", "socket", "endpoint", "context", "region"}
	tlsWords        = []string{"tls", "ssl", "ca-file", "cert", "key-file"}
)

func connectionGroup(f Field) string {
	switch {
	case f.Type.Sensitive() || slices.Contains(credentialNames, f.Name):
		return "credentials"
	case slices.ContainsFunc(tlsWords, func(w string) bool { return strings.Contains(f.Name, w) }):
		return "tls"
	case slices.Contains(networkNames, f.Name):
		return "network"
	}
	return "target"
}
