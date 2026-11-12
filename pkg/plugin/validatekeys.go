package plugin

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// ValidateKeys holds a plugin's config keys to the rules that let one value in
// an operator's file read the same under every capability that reads it:
//
//   - A plain key means one thing — its type, a single value or a list of one
//     kind counting as one, its default, its bounds and its options — to every
//     capability that declares it: it is one setting to the person who writes
//     it. `plugins.net.timeout` once drove five commands at once, an overall
//     timeout for ping and a limit per port for port, with a default of 10 for
//     one and 2 for the other, and setting it to speed one up changed four
//     others. Two capabilities that mean different things each name their own.
//   - A dotted key is one capability's own: the capability's ID without the
//     namespace, then the input's name. `products.limit` under eol.products,
//     never `catalogue.limit`, which reads as a capability that does not exist,
//     and the day a second capability reads the same setting it is a plain key
//     both share.
//   - A Renamed entry names a key no input declares any more, and the key an
//     input declares in its place.
//
// Run by the SDK's Serve, by sdktest and over rta's built-ins, and not by the
// host when it loads a plugin: these are rules about a declaration's shape,
// not its safety, and refusing a plugin built on an older SDK would take every
// installed plugin off the air on an rta upgrade.
func (p Plugin) ValidateKeys() error {
	type reading struct {
		capability string
		field      Field
	}
	byKey := map[string][]reading{}
	for _, c := range p.Capabilities {
		own := strings.TrimPrefix(c.ID, p.Name+".")
		for _, f := range c.Inputs {
			if f.Config == "" {
				continue
			}
			if dot := strings.LastIndexByte(f.Config, '.'); dot >= 0 && f.Config[:dot] != own {
				return fmt.Errorf("capability %q: input %q has config key %q, and a dotted key is one "+
					"capability's own: %q, or %q for a setting every capability reading it shares",
					c.ID, f.Name, f.Config, own+"."+f.Config[dot+1:], f.Config[dot+1:])
			}
			byKey[f.Config] = append(byKey[f.Config], reading{c.ID, f})
		}
	}
	for _, key := range slices.Sorted(maps.Keys(byKey)) {
		readers := byKey[key]
		first := readers[0]
		for _, r := range readers[1:] {
			if what := keyDiffers(first.field, r.field); what != "" {
				return fmt.Errorf("config key %q is read by %s and by %s with %s; a key is one setting to "+
					"whoever writes it, so it means one thing to both, or each names its own, as %q",
					key, first.capability, r.capability, what, strings.TrimPrefix(r.capability, p.Name+".")+"."+key)
			}
		}
	}
	for _, from := range slices.Sorted(maps.Keys(p.Renamed)) {
		to := p.Renamed[from]
		if readers, still := byKey[from]; still {
			return fmt.Errorf("renamed key %q is still read by %s: a renamed key is one nothing reads any more",
				from, readers[0].capability)
		}
		if _, read := byKey[to]; !read {
			return fmt.Errorf("renamed key %q is said to be read as %q, which no input declares", from, to)
		}
	}
	return nil
}

// keyDiffers is what two declarations of one config key disagree on, or "".
func keyDiffers(a, b Field) string {
	switch {
	case keyKind(a.Type) != keyKind(b.Type):
		return fmt.Sprintf("types %s and %s", a.Type, b.Type)
	case !sameKeyValue(a.Default, b.Default):
		return fmt.Sprintf("defaults %v and %v", a.Default, b.Default)
	case !sameKeyValue(a.Min, b.Min) || !sameKeyValue(a.Max, b.Max):
		return fmt.Sprintf("bounds %v to %v and %v to %v", a.Min, a.Max, b.Min, b.Max)
	case !sameKeySet(a.Options, b.Options):
		return fmt.Sprintf("options %v and %v", a.Options, b.Options)
	}
	return ""
}

// keyKind is the kind of value a key holds, a list of one kind counting as
// that kind: a key a capability reads as one namespace and another as a list
// of them is one setting, written either way.
func keyKind(t FieldType) FieldType {
	switch t {
	case StringSlice:
		return String
	case SecretSlice:
		return Secret
	}
	return t
}

// sameKeyValue reports whether two declarations state one value: a list of
// one is its element, and an empty value is no value.
func sameKeyValue(a, b any) bool { return reflect.DeepEqual(oneValue(a), oneValue(b)) }

func oneValue(v any) any {
	switch s := v.(type) {
	case []string:
		switch len(s) {
		case 0:
			return nil
		case 1:
			return s[0]
		}
	case string:
		if s == "" {
			return nil
		}
	}
	return v
}

// sameKeySet reports whether two lists of options offer the same choices, in
// whatever order each declaration lists them.
func sameKeySet(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}
