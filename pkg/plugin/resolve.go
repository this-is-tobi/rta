package plugin

import (
	"encoding/json"
	"os"
	"strings"
)

// Inputs are the layers Resolve merges, listed highest-precedence first — in
// the struct and in this comment, because the ordering *is* the security
// property and a positional parameter list is a swap the compiler cannot
// catch whose failure mode is a silent inversion.
//
// A nil field is a decision somebody typed, which is the argument the cfg
// parameter this replaced already made at length: three surfaces were found
// shadowing config by building values in a way that could never lose to it,
// and there is exactly one Resolve so a fourth cannot appear.
type Inputs struct {
	// Caller is what a person typed or an agent sent. It wins everything —
	// including the profile, so `--profile prod --host x` connects to x.
	Caller map[string]any

	// Profile is the operator's named connection, already resolved, keyed by
	// INPUT name rather than by Config key: internal/profile has done that
	// translation, and it is the only layer that may carry a Secret.
	Profile map[string]any

	// ProfileName is the profile in play, empty for none.
	//
	// Separate from Profile because an EMPTY profile map is still an active
	// profile: it is the *name*, not the contents, that switches the
	// namespace-wide environment layer off below.
	ProfileName string

	// Config is the operator's plugins:<ns@pin> section, keyed by Field.Config.
	Config map[string]any
}

// Resolve turns the values a surface collected into the values a handler
// actually runs with: declared defaults filled in, numbers normalised to one
// Go type, and declared bounds applied.
//
// It exists because four surfaces build a Request and each was doing a
// different subset of that work. The CLI got it right by accident — cobra
// bakes defaults into the flag set, so collectValues reads them back — while
// the TUI and the dashboard filled defaults only when the caller supplied no
// values at all, so a tile pinned as
//
//	{id: sys.ps, with: {limit: 5}}
//
// dropped every *other* declared default on the floor and handed the handler
// zero values it had no way to distinguish from real ones. The same config
// worked from a shell, which is the worst version of a bug: the user's file
// is right, the capability is right, and only one surface is wrong.
//
// Normalising types is the second half of the same problem. The config loader
// decodes untyped non-negative YAML integers as uint64, which Request.Int did
// not recognise, so it returned 0 — again silently, and again only on the
// surface that reads config.
//
// Every surface that runs a handler calls this. Nothing downstream has to
// know which of the values were declared, defaulted, or clamped.
//
// Precedence is caller, then profile, then the namespace-wide environment
// fallback, then config, then Default. A handler reads req.String("host") and
// cannot tell which of the five it got, which is the point: a config-backed
// input is an ordinary input, and so is a profile-backed one.
func Resolve(c Capability, in Inputs) map[string]any {
	out := make(map[string]any, len(c.Inputs)+len(in.Caller))
	for _, f := range c.Inputs {
		if f.Default != nil {
			out[f.Name] = f.Default
		}
	}
	// Between the defaults and the caller's values, so it beats the first and
	// loses to the second — by position, not by a check. An earlier draft also
	// skipped an input the caller had supplied, which reads like the rule and
	// is dead: the loop below overwrites whatever this one wrote. A mutation
	// run removed it and nothing failed, which is the only way that kind of
	// line gets found.
	//
	// Only for inputs that declared a key: config cannot reach an input whose
	// author did not offer it, which is what keeps the reachable set a
	// property of the declaration — checkable before the process runs, and
	// printable by `rta explain` — rather than of whatever is in a file.
	for _, f := range c.Inputs {
		if f.Config == "" {
			continue
		}
		if v, ok := lookupConfig(in.Config, f.Config); ok {
			out[f.Name] = v
		}
	}
	// Local inputs that opted into it (EnvFallback), from the host's own
	// environment, under a name derived from the plugin's namespace:
	// RTA_<NS>_<INPUT>.
	//
	// This is the one way an external plugin can obtain a credential, and
	// until now there was none. The plugin process inherits an allowlist of
	// seven variable names (internal/pluginhost/env.go) and RTA_* is
	// deliberately not among them — a plugin that never reads a file must not
	// be able to exfiltrate an AWS session by starting up. Local meant only
	// "never accepted from a remote caller"; nothing ever *supplied* one, so
	// an external plugin declaring a Local Secret received the empty string.
	// Verified by building one: PGPASSWORD empty, the Local input empty.
	//
	// kv has resolved RTA_KV_PASSPHRASE and RTA_KV_IDENTITY by hand since it
	// was written, which is this convention already — implemented once,
	// written down nowhere, and available only to a built-in because a
	// built-in runs inside rta and sees rta's whole environment. That is a
	// second-class plugin, and there are none of those. Lifting it here
	// makes it the host's job for everyone.
	//
	// The name is derived, never declared, and that is the security property:
	// a plugin can only ever read variables under its own namespace's prefix.
	// A declared Env field would let a hostile declaration name
	// AWS_SECRET_ACCESS_KEY and have the host hand it over.
	//
	// Gated on EnvFallback, not on Local alone — a real bug this closed:
	// kv.get's --out is Local so an MCP caller can never
	// aim a revealed secret at an arbitrary file, but every Local field used
	// to resolve from the environment unconditionally, so an operator's own
	// RTA_KV_OUT silently redirected a legitimate, per-key-granted kv_get
	// call's response to disk instead of returning it — the exact thing
	// Local's own doc comment says a grant does not authorize. EnvFallback
	// is for the fields that actually are credentials (a passphrase, the key
	// that unlocks one); a field that only chooses a destination on this
	// machine should never be filled from anywhere but an explicit caller.
	//
	// Below the caller's values, because an explicitly typed credential beats
	// an ambient one, and above config, which refuses Secret inputs outright.
	//
	// **Skipped entirely while a profile is active**, and that is a security
	// rule rather than a tidiness one. RTA_<NS>_<INPUT> is bound to a
	// namespace, so it follows the connection wherever a profile points it:
	// an operator with RTA_PG_PASSWORD exported for their own database, whose
	// agent then names a profile aimed somewhere else, would have the host
	// pair a destination somebody else chose with a credential they did not
	// supply for it — the credential-redirect hole Local closes, rebuilt one
	// layer up.
	//
	// The whole layer, not just the inputs the profile happened to fill.
	// Skipping only the overlap leaves exactly the same shape: a profile that
	// sets `host` and no password still redirects the ambient one. A profile
	// carries its own credential (RTA_PROFILE_<P>_<INPUT>, which
	// internal/profile reads into in.Profile above) or it carries none and
	// the connection fails saying so.
	if in.ProfileName == "" {
		for _, f := range c.Inputs {
			if !f.Local || !f.EnvFallback {
				continue
			}
			if v, ok := os.LookupEnv(LocalEnvVar(c.ID, f.Name)); ok {
				out[f.Name] = v
			}
		}
	}
	// The operator's named connection, above config and the ambient
	// environment because naming one is a more specific statement than either,
	// and below the caller because a person who typed --host meant it.
	//
	// Gated on ProfileFillable so the reachable set stays a property of the
	// declaration: a profile can never fill a Path, nor the input a capability
	// declares as its Scope, nor anything its author offered neither to
	// configuration nor as a credential.
	for _, f := range c.Inputs {
		if !ProfileFillable(c, f) {
			continue
		}
		if v, ok := in.Profile[f.Name]; ok {
			out[f.Name] = v
		}
	}
	for k, v := range in.Caller {
		out[k] = v
	}

	byName := make(map[string]Field, len(c.Inputs))
	for _, f := range c.Inputs {
		byName[f.Name] = f
	}
	for name, v := range out {
		f, declared := byName[name]
		if !declared {
			continue
		}
		switch f.Type {
		case Int:
			if n, ok := toInt(v); ok {
				out[name] = clampInt(n, f)
			}
		case Float:
			if n, ok := toFloat(v); ok {
				out[name] = clampFloat(n, f)
			}
		}
	}
	return out
}

// toInt accepts every shape an integer arrives in. YAML gives uint64, JSON
// gives float64, cobra gives int, and a plugin's own Default is whatever its
// author wrote — so the set is wider than it looks, and a value this does not
// recognise is left alone rather than replaced with a confident zero.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint:
		return int(n), true
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		return int(n), true
	case float32:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f, true
		}
	default:
		if i, ok := toInt(v); ok {
			return float64(i), true
		}
	}
	return 0, false
}

func clampInt(n int, f Field) int {
	if lo, ok := toInt(f.Min); ok && n < lo {
		n = lo
	}
	if hi, ok := toInt(f.Max); ok && n > hi {
		n = hi
	}
	return n
}

func clampFloat(n float64, f Field) float64 {
	if lo, ok := toFloat(f.Min); ok && n < lo {
		n = lo
	}
	if hi, ok := toFloat(f.Max); ok && n > hi {
		n = hi
	}
	return n
}

// lookupConfig walks a dotted key through nested maps.
//
// Both map shapes, because the same file reaches here two ways: goccy-yaml
// decodes a nested block as map[string]any, and anything that has been
// through a JSON round trip or an older decoder can hand back
// map[any]any. A key that runs out of map before it runs out of segments is
// simply absent — a config file that says something this capability does not
// understand is not an error at the point of use, it is reported once by
// internal/pluginconf against the whole catalogue.
func lookupConfig(cfg map[string]any, key string) (any, bool) {
	if cfg == nil || key == "" {
		return nil, false
	}
	cur := any(cfg)
	for _, seg := range strings.Split(key, ".") {
		switch m := cur.(type) {
		case map[string]any:
			v, ok := m[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case map[any]any:
			v, ok := m[seg]
			if !ok {
				return nil, false
			}
			cur = v
		default:
			return nil, false
		}
	}
	// A nested block is a namespace, not a value: handing a map to
	// Request.String would stringify a Go map into somebody's connection.
	switch cur.(type) {
	case map[string]any, map[any]any, nil:
		return nil, false
	}
	return cur, true
}

// LocalEnvVar is the environment variable a Local input is filled from:
// RTA_<NAMESPACE>_<INPUT>, uppercased, with dashes as underscores.
//
// Exported because it is a contract with plugin authors and with operators,
// not an implementation detail: `rta explain` prints it, so the answer to
// "how do I give this plugin its password" is on the page describing the
// capability rather than in a plugin's README.
func LocalEnvVar(capID, input string) string {
	ns, _, _ := strings.Cut(capID, ".")
	return "RTA_" + envToken(ns) + "_" + envToken(input)
}
