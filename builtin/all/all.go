// Package all assembles the built-in plugins into a registry.
//
// It exists so that "which plugins ship with rta" is stated once, in a
// package that depends on nothing but the plugins themselves. internal/app
// used to own the list, which put it downstream of every renderer and made
// it unreachable from any test living inside one — so the checks that most
// want the real catalogue (does every dashboard tile name a capability that
// exists?) could only run against a hand-built stand-in that drifts.
package all

import (
	rtaagent "github.com/this-is-tobi/rta/builtin/agent"
	"github.com/this-is-tobi/rta/builtin/audit"
	"github.com/this-is-tobi/rta/builtin/cert"
	"github.com/this-is-tobi/rta/builtin/codec"
	rtadebug "github.com/this-is-tobi/rta/builtin/debug"
	"github.com/this-is-tobi/rta/builtin/eol"
	rtafs "github.com/this-is-tobi/rta/builtin/fs"
	"github.com/this-is-tobi/rta/builtin/gen"
	rtagit "github.com/this-is-tobi/rta/builtin/git"
	"github.com/this-is-tobi/rta/builtin/grant"
	rtahttp "github.com/this-is-tobi/rta/builtin/http"
	"github.com/this-is-tobi/rta/builtin/keys"
	"github.com/this-is-tobi/rta/builtin/kv"
	rtalock "github.com/this-is-tobi/rta/builtin/lock"
	rtanet "github.com/this-is-tobi/rta/builtin/net"
	"github.com/this-is-tobi/rta/builtin/note"
	rtaoperator "github.com/this-is-tobi/rta/builtin/operator"
	"github.com/this-is-tobi/rta/builtin/sys"
	rtatime "github.com/this-is-tobi/rta/builtin/time"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// Registry returns every built-in plugin, registered and indexed.
//
// conf answers what the operator's configuration states for a capability —
// the app layer passes its pin-matched resolver, tests pass nil (meaning
// "nothing configured") or their own. It exists for the one built-in that
// runs *other* capabilities: the ai plugin's tool bridge hands each tool
// call the same config every surface would, and a parameter rather than
// package state is what keeps two registries in one test process apart.
func Registry(conf func(plugin.Capability) map[string]any) (*registry.Registry, error) {
	reg := registry.New()
	for _, p := range []plugin.Plugin{
		sys.Plugin(),
		cert.Plugin(),
		rtanet.Plugin(),
		rtahttp.Plugin(),
		audit.Plugin(),
		eol.Plugin(),
		rtafs.Plugin(),
		rtagit.Plugin(),
		note.Plugin(),
		kv.Plugin(),
		gen.Plugin(),
		codec.Plugin(),
		rtatime.Plugin(),
		rtadebug.Plugin(),
		keys.Plugin(),
		// agent is the operator's window onto what AI agents did and what
		// they are asking for; grant is the standing policy beside it, and
		// is handed the catalogue so what can be granted is derived from
		// what is registered, never listed twice.
		rtaagent.Plugin(),
		grant.Plugin(reg.Capabilities),
		// operator is the person's other half of the same story: the identity
		// with which they manage rta servers that are not this machine.
		rtaoperator.Plugin(),
		// lock is the emergency brake beside both: freeze one principal
		// across the network surfaces now, without restarting anything.
		rtalock.Plugin(),
	} {
		if err := reg.Register(p); err != nil {
			return nil, err
		}
	}
	return reg, nil
}
