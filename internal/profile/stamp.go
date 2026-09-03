package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/internal/config"
)

// Stamp fingerprints everything about an environment that a resolution of it
// depends on: the connections it states, and nothing else about the file they
// were read out of.
//
// It exists because a long-running surface has to know when to resolve again,
// and the obvious key — the environment's *name* — is the one thing about an
// environment that does not change when its meaning does. A profile edited in
// place keeps its name, so a cache keyed on the name never expires: the TUI
// bound `proj1-staging` once at the switch and held those values for the rest
// of the session, so changing that environment's host, endpoint or region left
// every form seeded from the old connection and every command *running against
// it*, until the operator quit and relaunched. Reported after exactly that.
//
// A stamp fixes it by construction rather than by discipline. The alternative
// was for each of the seven places that write a profile to invalidate the
// cache, which is the arrangement that produced the bug: the plugin-config
// editor remembered, the profile editors did not, and `rta profile edit` in
// another terminal could never have. Describing the thing rather than naming
// it covers all of them, including the ones not written yet.
//
// Deterministic across two reads of the same file: every map is walked in
// sorted key order, and every part is length-prefixed so that no value can
// impersonate another profile's stamp by containing a separator.
func Stamp(p config.Profile) string {
	h := sha256.New()
	write := lengthPrefixed(h)
	// The activation window belongs to the whole profile rather than to any
	// connection, and the TUI's badge has to notice it change. ConnStamp
	// deliberately excludes it — see there.
	write("ttl", p.TTL)
	// Same reason as the TTL directly above, and the same narrowing: the badge
	// is what this stamp exists to keep current, and the colour is now part of
	// it. ConnStamp still excludes both, so nothing a grant is bound to moves
	// when an operator recolours an environment.
	write("color", p.Color)
	for _, key := range p.PluginKeys() {
		write("plugin", key, ConnStamp(key, p.Plugins[key]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ConnStamp fingerprints one plugin's connection inside an environment: the
// key it is written under, and everything that key's resolution reads.
//
// **Everything Stamp covers except the profile's TTL, and one connection
// rather than all of them.** Both narrowings are what makes it usable as the
// thing a grant is bound to:
//
//   - `p.TTL` is the operator's activation window for `rta use`. It never
//     reaches Bind, Fill or Resolve and cannot change where a call goes, so a
//     grant pinned to a stamp carrying it would be revoked by editing `ttl: 8h`
//     to `4h` — and the safety direction is backwards too, since the selection
//     only ever subtracts.
//   - A grant's Target is always exactly one namespace, and resolution reads
//     only `p.For(ns)`. Stamping the whole environment would let an edit to a
//     profile's `s3` entry revoke a live `pg` grant that the edit provably
//     cannot affect.
//
// The key is included rather than just the namespace, because the key carries
// the artifact pin: `pg@1b6093cf90ce` re-pinned after a rebuild is a different
// connection, resolved through a different binary.
//
// Stamp is written in terms of this so the TUI's cache key and the grant gate
// cannot drift. This repo has twice recorded the same bug — a check on the
// reporting path that was not on the path using the value — and twice the fix
// was to make the two share the function.
//
// **What it cannot see, and what that means for the claim it supports.**
// Secret *references* are covered and secret values are not, deliberately: the
// comparison happens before profile.Fill, precisely so a credential is never
// fetched for a call about to be refused, and a value-covering stamp would
// unlock the store on every gated call. Nor can any hash over a config file
// see `RTA_PROFILE_<NAME>_<INPUT>`, which repoints a connection with the file
// untouched. So this answers "the configured connection this was issued
// against has not changed" — not "the resolved connection has not changed" —
// and nothing built on it may claim more.
func ConnStamp(key string, c config.Connection) string {
	h := sha256.New()
	write := lengthPrefixed(h)
	// Both tunnel schemes, labelled apart, and whether the far end of one
	// speaks TLS on its own: each is "where this connection goes" — a
	// scheme flip changes it exactly as a host flip would, which is the
	// whole reason it exists. This line is the enumeration the Tunnelled
	// predicate cannot cover for — a field added to Connection without a
	// write here is a repoint no standing grant notices, so the drift test
	// in stamp_test.go counts Connection's fields.
	//
	// `secrets-from:` is in the stamp for a sharper version of the same
	// reason. It names the cluster and namespace a credential is read out of,
	// so editing it repoints *which* credential authenticates the call while
	// every other line stays identical — `homelab/dev` to `homelab/prod` is
	// one word, and a grant issued against the first would otherwise keep
	// authorizing calls made with the second. It changes where a call goes as
	// surely as a host does, one layer further in.
	write("key", key, "kube", c.Kube, "ssh", c.SSH,
		"secretsFrom", c.SecretsFrom, "tunnelTLS", fmt.Sprintf("%t", c.TunnelTLS))
	for _, k := range sortedKeys(c.Set) {
		write("set", k, canonical(c.Set[k]))
	}
	for _, k := range sortedKeys(c.Secrets) {
		// The reference, not the value.
		write("secret", k, c.Secrets[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ConnStampFor is ConnStamp reached through a whole config: the stamp of the
// entry profile `name` holds for `namespace`.
//
// **Empty whenever there is nothing to stamp** — no name, no such profile, no
// entry for that namespace — and that is the right zero for what reads it. A
// grant carrying a profile always carries a non-empty pin, so an environment
// that has been deleted, renamed, or stripped of the plugin stops matching,
// which is the fail-closed direction. A caller that wants "is there a
// connection here at all" has config.Profile.Covers for it.
//
// One function rather than one per caller: the MCP gate and `rta grant list`
// have to agree exactly about which connection a grant names, or a row is
// marked live by one rule and refused by another. This repo has recorded that
// drift twice — an advisory checkPin, then an advisory checkSet — and both
// times the fix was to make the two paths share the function.
func ConnStampFor(cfg config.Config, ref, namespace string) string {
	if ref == "" {
		return ""
	}
	// The ref may carry an instance — `staging/analytics` — and the stamp is
	// then that instance's, which is the whole point of per-instance grants:
	// a pin over the analytics connection must not keep matching after the
	// grant's words are re-aimed at the main one. With no instance, For's
	// default rules apply, and its refusal to pick among several labeled
	// entries returns "" here — the fail-closed direction, same as a deleted
	// profile.
	name, instance := config.SplitRef(ref)
	p, ok := cfg.Profiles[name]
	if !ok {
		return ""
	}
	var (
		key  string
		conn config.Connection
	)
	if instance != "" {
		key, conn, ok = p.ForInstance(namespace, instance)
	} else {
		key, conn, ok = p.For(namespace)
	}
	if !ok {
		return ""
	}
	return ConnStamp(key, conn)
}

// appendPart writes one encoded element length-prefixed, so a concatenation of
// them can only be read back one way.
func appendPart(b *strings.Builder, s string) {
	fmt.Fprintf(b, "%d:%s", len(s), s)
}

// lengthPrefixed writes parts so that no value can impersonate another by
// containing a separator.
func lengthPrefixed(h io.Writer) func(parts ...string) {
	return func(parts ...string) {
		for _, s := range parts {
			fmt.Fprintf(h, "%d:%s\n", len(s), s)
		}
	}
}

// canonical renders a YAML value so that two distinct documents cannot share
// an encoding.
//
// **Type-tagged, which `%v` is not.** `fmt.Sprintf("%v", …)` over a
// `map[string]any` decoded from YAML prints `5432` for the integer and for
// the string, and `[a]` for the one-element list and for the literal text.
// Length-prefixing defends the joins *between* parts and does nothing about a
// collapse *inside* one. For a cache that is harmless — both coerce the same
// in Resolve — but the bar for something a grant is bound to is that no two
// distinct documents share a digest, and `port: "5432"` against `port: 5432`
// is exactly the substitution a pin exists to notice.
//
// **Length-prefixed rather than separator-joined**, for the reason the
// top-level writer already is. Joining elements with a separator byte and
// trusting no value to contain it is a rule the values get to break: with
// `\x1f` between list elements, `hosts: ["a", "b"]` and `hosts:
// ["a\x1fstr:b"]` encode identically, and with `\x1e` between a key and its
// value a two-key map collapses onto a one-key map carrying the rest as text.
// Both are reachable — YAML writes those bytes from a double-quoted `\xNN`
// escape, nothing upstream rejects a container under a fillable key, and both
// documents load and resolve. A separator inside a value is exactly the trap
// the length prefix exists to close, and it had to be closed here too because
// the prefix only ever defended the joins *between* the top-level parts.
//
// Maps are walked in sorted key order, so the same file stamps the same way
// twice.
func canonical(v any) string {
	switch t := v.(type) {
	case nil:
		return "nil"
	case string:
		return "str:" + t
	case bool:
		return fmt.Sprintf("bool:%t", t)
	case int:
		return fmt.Sprintf("int:%d", t)
	case int64:
		return fmt.Sprintf("int:%d", t)
	case float64:
		return fmt.Sprintf("float:%v", t)
	case []any:
		var b strings.Builder
		b.WriteString("list:")
		for _, e := range t {
			appendPart(&b, canonical(e))
		}
		return b.String()
	case map[string]any:
		var b strings.Builder
		b.WriteString("map:")
		for _, k := range sortedKeys(t) {
			appendPart(&b, canonical(k))
			appendPart(&b, canonical(t[k]))
		}
		return b.String()
	default:
		// A shape this does not name yet. %#v carries the Go type, so an
		// unrecognised value still cannot collide with a string that happens
		// to print the same way.
		return fmt.Sprintf("go:%#v", t)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
