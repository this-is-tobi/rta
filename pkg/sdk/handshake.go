// Package sdk is what a plugin author imports. One function — Serve — turns
// a plugin.Plugin declaration into a process rta can launch.
//
// The whole point is that the declaration is the same value a built-in
// writes. builtin/fs returns a plugin.Plugin; an external plugin returns a
// plugin.Plugin and passes it here. Nothing about a capability changes on the
// way across a process boundary, which is what makes "move a built-in out of
// the binary" a refactor rather than a rewrite — and what keeps the four
// renderers from ever learning that some plugins are remote.
//
// What an author does NOT do here is decide anything about security. There is
// no confinement option, no environment knob, no descriptor choice: the host
// makes every one of those, because a plugin that could ask to be
// less confined is a plugin that will.
package sdk

import (
	goplugin "github.com/hashicorp/go-plugin"
)

// Handshake is the mutual identification rta and its plugins perform before
// any gRPC traffic. A process that does not print this exact line is not an
// rta plugin, and go-plugin refuses it before the user sees a timeout.
//
// ProtocolVersion is not the same number as `proto/v1`. It gates the
// *handshake* — the transport and the plugin-set names — and moves only when
// a plugin built against an older rta could no longer be launched at all.
// Additive changes inside rta.v1 are proto's problem and travel under proto's
// rules (unknown fields ignored, unknown enum values refused by name), which
// is why the wire package returns "unknown" lists rather than errors.
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "RTA_PLUGIN",
	MagicCookieValue: "rule-them-all",
}

// PluginSetName is the single entry in every plugin set rta serves or dials.
//
// One name, not one per plugin: go-plugin's map keys select *which service*
// inside a process, and rta's answer is always "the one that speaks
// PluginService". A plugin's own name comes from Describe, over the wire,
// where the host can validate it — not from a map key the host would have to
// know before it launched anything.
const PluginSetName = "rta"
