package plugin

// Naming the server a call reached, once the call is over.
//
// Every connection plugin that hands its reader a next call — the restore
// that puts a dump back, the listing a refusal offers, the removal a copy
// says to run — has to name the server again, and through a forward the
// host opened (Tunnel) the address its handler was given is 127.0.0.1 and a
// port that closed with the call. Each plugin wrote the same two answers to
// that, a phrase and the arguments, and each wrote them again for every
// receipt it had; one that left the profile out sent its reader's paste to
// whatever the configuration named, which for a removal was a delete on a
// server the copy never reached.

// Reached names the server this call reached as its reader reaches it again:
// endpoint, the address the handler was given, alone when no profile filled
// the call, and beside the profile that did — and the profile alone when the
// host reached the server through a forward it opened on that profile, whose
// end, 127.0.0.1 and a port, closed with the call. For a sentence's object:
// "copied to <Reached>", "would restore into <Reached>".
func (r Request) Reached(endpoint string) string {
	switch profile := r.Profile(); {
	case profile == "":
		return endpoint
	case r.Tunnel() == TunnelNone:
		return endpoint + " (profile " + profile + ")"
	default:
		return "profile " + profile + " (through its " + string(r.Tunnel()) + ": forward)"
	}
}

// ReachArgs is the arguments a Call takes to reach the server this call
// reached again: the profile whenever there was one, then endpoint — the
// inputs naming the server, with the values the handler was given — only
// when the host opened no forward (Tunnel is TunnelNone). Through a forward
// they are its end, gone with the call, and the profile reaches the server
// again; reached directly they stay, since they may be ones the caller typed
// over the profile's, which the profile alone would not reach. The profile
// is given through a forward or not, since the credentials the call used may
// be the profile's and no other layer holds them.
//
// The host spells the profile on each surface: --profile at a terminal, the
// "profile" argument over MCP, the form's profile box in the TUI. What else
// the server needs to be reached the same way — a TLS mode, a CA file, the
// name a certificate is checked for — the plugin appends; and over MCP only
// what an agent can give, since a Local input is not one.
func (r Request) ReachArgs(endpoint ...Arg) []Arg {
	var args []Arg
	if profile := r.Profile(); profile != "" {
		args = append(args, Arg{Name: "profile", Value: profile})
	}
	if r.Tunnel() == TunnelNone {
		args = append(args, endpoint...)
	}
	return args
}
