package plugin

import "time"

// The two paces a dashboard tile runs at when its capability does not state one.
//
// **A tile's pace follows what a run costs, and what a run costs is mostly where
// it goes.** Reading /proc is free and nobody is told, so a few seconds is right.
// A capability that reaches a configured connection — a cluster, a database, an
// object store, a vault — is a round trip to a service somebody else runs and
// logs, and often several: a pg tile is two kubectl calls and a login, and
// behind a bastion that audits every Kubernetes request each tile is audit
// events for as long as the screen is drawn. The dashboard used to give both the
// same few seconds, and every plugin that wanted better had to say so in its own
// code; the ones that did not (vault, keycloak, pg, cnpg, a pod list) polled a
// production service at the pace of a clock.
//
// Derived from Profilable rather than declared, for the reason that function
// gives: no proto field, nothing for a plugin author to remember, and a plugin
// already installed gets the right pace without an upgrade.
const (
	// PaceLocal is the host's own pace: the timer's tick, which a tile that
	// reads the machine it is on runs at.
	PaceLocal = 5 * time.Second
	// PaceRemote is the pace of a tile that reaches a configured connection.
	// Thirty seconds is what kv.overview keeps for a decrypt, and what a glance
	// at a service's health needs: a state that matters lasts longer than that,
	// and one that does not is a flicker the tile should not draw.
	PaceRemote = 30 * time.Second
)

// Pace is how often a dashboard tile of c is re-run: what the capability
// declared (Refresh), or else the pace its kind of work gets (PaceRemote for one
// that reaches a connection, PaceLocal for the rest).
//
// A declared pace is the capability's statement of what a run costs, which the
// host cannot see: a configuration can lengthen it and never shorten it (see
// ClampPace).
func (c Capability) Pace() time.Duration {
	switch {
	case c.Refresh > 0:
		return c.Refresh
	case Profilable(c):
		return PaceRemote
	}
	return PaceLocal
}

// ClampPace is the pace a tile runs at when a configuration asks for want.
//
// Two different things stand under the request. The pace a capability declared
// (Refresh) is a floor: it is the capability's statement of what a run costs,
// which the host cannot see, and the operator's file is the wrong place to turn
// eol.watch — a request per product to a public API — into a beacon. A
// configuration lengthens it freely, and cannot shorten it. The pace a
// capability merely gets for the kind of work it does (PaceRemote) is a default:
// somebody who wants a database tile every ten seconds may have it, down to the
// timer's own tick, since nothing here said that a run is dear.
//
// want <= 0 is "nothing stated" and yields Pace. The second result says the
// request was cut, for a message that tells the person which of their lines did
// not take.
func ClampPace(c Capability, want time.Duration) (time.Duration, bool) {
	if want <= 0 {
		return c.Pace(), false
	}
	floor := PaceLocal
	if c.Refresh > floor {
		floor = c.Refresh
	}
	if want < floor {
		return floor, true
	}
	return want, false
}
