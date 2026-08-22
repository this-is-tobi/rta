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
	"github.com/this-is-tobi/rule-them-all/builtin/audit"
	"github.com/this-is-tobi/rule-them-all/builtin/cert"
	"github.com/this-is-tobi/rule-them-all/builtin/codec"
	rtadebug "github.com/this-is-tobi/rule-them-all/builtin/debug"
	rtafs "github.com/this-is-tobi/rule-them-all/builtin/fs"
	"github.com/this-is-tobi/rule-them-all/builtin/gen"
	rtagit "github.com/this-is-tobi/rule-them-all/builtin/git"
	"github.com/this-is-tobi/rule-them-all/builtin/grant"
	rtahttp "github.com/this-is-tobi/rule-them-all/builtin/http"
	"github.com/this-is-tobi/rule-them-all/builtin/keys"
	"github.com/this-is-tobi/rule-them-all/builtin/kv"
	rtanet "github.com/this-is-tobi/rule-them-all/builtin/net"
	"github.com/this-is-tobi/rule-them-all/builtin/note"
	"github.com/this-is-tobi/rule-them-all/builtin/sys"
	"github.com/this-is-tobi/rule-them-all/builtin/todo"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Registry returns every built-in plugin, registered and indexed.
func Registry() (*registry.Registry, error) {
	reg := registry.New()
	for _, p := range []plugin.Plugin{
		sys.Plugin(),
		cert.Plugin(),
		rtanet.Plugin(),
		rtahttp.Plugin(),
		audit.Plugin(),
		rtafs.Plugin(),
		rtagit.Plugin(),
		todo.Plugin(),
		note.Plugin(),
		kv.Plugin(),
		gen.Plugin(),
		codec.Plugin(),
		rtadebug.Plugin(),
		keys.Plugin(),
		// grant is about the others: it is handed the catalogue so what can
		// be granted is derived from what is registered, never listed twice.
		grant.Plugin(reg.Capabilities),
	} {
		if err := reg.Register(p); err != nil {
			return nil, err
		}
	}
	return reg, nil
}
