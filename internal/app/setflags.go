package app

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// collectPairs splits `key=value` arguments into the keys in the order they
// were first stated and the values each was given, refusing the shapes neither
// block can hold.
//
// Collected before any of it is typed, because a StringSlice input is stated by
// repeating the key and the whole list has to be in hand first — and an
// allowlist is stated the same way, one argument per allowed value.
func collectPairs(pairs []string, secrets map[string]bool, w flagWords) ([]string, map[string][]string, *view.Error) {
	order := make([]string, 0, len(pairs))
	raw := map[string][]string{}
	for _, pair := range pairs {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			// The argument is deliberately not echoed. Somebody who typed a
			// value where a pair belongs has already put it on a command
			// line; rta will not put it anywhere else.
			return nil, nil, view.Errorf(w.shape,
				"one --%s argument is not a `key=value` pair", w.flag).
				WithHint("write it as `--" + w.flag + " " + w.example + "`")
		}
		k = strings.TrimSpace(k)
		if k == "" {
			return nil, nil, view.Errorf(w.shape, "one --%s argument names no key", w.flag).
				WithHint("write it as `--" + w.flag + " " + w.example + "`")
		}
		if secrets[k] {
			return nil, nil, w.secret(k)
		}
		if v == "" {
			return nil, nil, w.empty(k)
		}
		if _, seen := raw[k]; !seen {
			order = append(order, k)
		}
		raw[k] = append(raw[k], v)
	}
	return order, raw, nil
}

// typedSetValue turns the text a flag carries into the type f declares. key
// is what was typed before the `=`: a config key for a profile, an input name
// for a tile.
func typedSetValue(f plugin.Field, key string, values []string) (any, *view.Error) {
	raw := values[len(values)-1]
	switch f.Type {
	case plugin.StringSlice, plugin.SecretSlice:
		return append([]string{}, values...), nil
	case plugin.Int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			want := "a whole number"
			switch bounds := f.Bounds(); {
			case bounds != "":
				want += " " + bounds
			case errors.Is(err, strconv.ErrRange):
				// Digits, and too many of them, on either side of zero:
				// "takes a whole number" is what the person already wrote.
				want += " from " + strconv.Itoa(math.MinInt) + " to " + strconv.Itoa(math.MaxInt)
			}
			return nil, badSetValue(f, key, want, setExample(f, "1"))
		}
		return n, nil
	case plugin.Float:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			want := "a number"
			if bounds := f.Bounds(); bounds != "" {
				want += " " + bounds
			}
			return nil, badSetValue(f, key, want, setExample(f, "1.5"))
		}
		return n, nil
	case plugin.Duration:
		if _, err := plugin.ParseDuration(raw); err != nil {
			want := "a duration"
			if bounds := f.Bounds(); bounds != "" {
				want += " " + bounds
			}
			return nil, badSetValue(f, key, want, setExample(f, "30s"))
		}
		return raw, nil
	case plugin.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			// `yes` and `on` land here, and that is the point: they are what
			// somebody writes when they mean true, and YAML 1.2 reads both as
			// text — so the file spelling of this mistake silently disables
			// whatever the field turns on.
			return nil, badSetValue(f, key, "`true` or `false`", "true")
		}
		return b, nil
	case plugin.Secret:
		// Unreachable: a Secret cannot be a config key, so it cannot be found
		// by config key here. Kept because "unreachable" is a claim about
		// another package, and the cost of being wrong is a credential in a
		// plaintext file.
		return nil, view.Errorf("core.profile.set.secret",
			"%q is a credential and cannot be stated with --set", f.Name).
			WithHint("store it and reference it with `--secret`")
	}
	return raw, nil
}

// badSetValue reports a value the declared type cannot hold, without ever
// repeating the value: this runs over something somebody typed on a command
// line, and the reason it did not parse is not a reason to print it again.
func badSetValue(f plugin.Field, key, want, example string) *view.Error {
	return view.Errorf("core.profile.set.type",
		"%s is declared %s, and takes %s", key, f.Type, want).
		WithHint("write it as `--set " + key + "=" + example + "`")
}

// setExample is a value for a hint to show, one heldToDeclaration then takes:
// the declared default, or the edge of the range, or fallback. The hint said
// `--set timeout=5432` for every integer, and following it for net's timeout
// (1 to 300) was refused as out of range.
func setExample(f plugin.Field, fallback string) string {
	for _, v := range []any{f.Default, f.Min, f.Max} {
		if _, ok := f.Range(v); v != nil && ok {
			return plugin.NumberText(v)
		}
	}
	return fallback
}

// parseSecretFlags turns `--secret input=ref` into the `secrets:` block.
//
// The scheme is what makes this a reference rather than a value, so a missing
// one is refused here rather than left to the loader — and refused **without
// echoing the argument**, because the single most likely thing to be sitting
// where a scheme should be is the credential itself.
func parseSecretFlags(pairs []string) (map[string]string, *view.Error) {
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		input, ref, ok := strings.Cut(pair, "=")
		input = strings.TrimSpace(input)
		if !ok || input == "" {
			return nil, view.Errorf("core.profile.secret.shape",
				"one --secret argument is not an `input=reference` pair").
				WithHint("write it as `--secret password=kv:staging-db-password`")
		}
		out[input] = strings.TrimSpace(ref)
	}
	// Asked the way the loader asks it, so the two cannot disagree about what
	// counts as a source.
	if bad := (config.Connection{Secrets: out}).BadSecretRefs(); len(bad) > 0 {
		named := make([]string, 0, len(bad))
		for _, b := range bad {
			named = append(named, strings.TrimPrefix(b, "secrets."))
		}
		return nil, view.Errorf("core.profile.secret.value",
			"--secret takes a reference to a stored credential, not the credential: %s",
			strings.Join(named, ", ")).
			WithHint("store it first — `rta kv set <entry> --file <path>` — then " +
				"`--secret <input>=kv:<entry>`. If what you passed was the credential " +
				"itself, it is in this shell's history now")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
