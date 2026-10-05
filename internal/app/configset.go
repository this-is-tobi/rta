package app

import (
	"os"
	"slices"
	"strings"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/shellquote"
	"github.com/this-is-tobi/rta/pkg/view"
)

// refuseUnhonouredKey stops a write that would land where nothing reads it.
// config.Path falls back to ./.rta.yaml when there is no config directory, and
// that file is honoured for the output format and the palette alone: anything
// else in a file nobody named is ignored, because a cloned repository must not
// be able to state a plugin's host or arrange a screen.
func refuseUnhonouredKey(key configKey) *view.Error {
	if key.Honoured || config.TrustedPath() {
		return nil
	}
	return view.Errorf("core.config.untrusted",
		"%s is not honoured from %s, so writing it there would do nothing", key.Name, config.Path()).
		WithHint("set $RTA_CONFIG to the file you mean — a config file found in the working " +
			"directory states nothing but the output format and the colours, because a cloned " +
			"repository must not")
}

// runConfigSet is `rta config set`.
//
// Idempotent on purpose, as `rta profile set` is: it states what the key is, so
// running it twice is running it once, and a script that sets a key on every
// boot leaves the file as it found it.
func runConfigSet(reg *registry.Registry, raw string, values []string, dryRun bool) (view.View, *view.Error) {
	key, verr := resolveConfigKey(reg, raw)
	if verr != nil {
		return nil, verr
	}
	value, verr := key.parse(values)
	if verr != nil {
		return nil, verr
	}
	if verr := refuseUnhonouredKey(key); verr != nil {
		return nil, verr
	}

	var before, after any
	var had, has bool
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		before, had = key.read(cfg)
		key.write(&cfg, value)
		after, has = key.read(cfg)
		changed := had != has || strings.Join(configValueLines(before), "\n") != strings.Join(configValueLines(after), "\n")
		return cfg, changed && !dryRun
	}); err != nil {
		return nil, view.AsError(err, "core.config.write")
	}
	if had == has && strings.Join(configValueLines(before), "\n") == strings.Join(configValueLines(after), "\n") {
		return view.KeyValue{Pairs: slices.Concat([]view.Pair{{Key: "unchanged",
			Value: key.Name + " " + configStated(key, after, has) + " — nothing written to " + config.Path()}},
			outputOutranked(key, strings.Join(configValueLines(after), "")))}, nil
	}

	label, verb := "wrote", "set "+key.Name+" to "+strings.Join(configValueLines(after), ", ")
	if !has {
		verb = key.Name + " " + configStated(key, after, has) + ", so the key is left out of the file"
	}
	if dryRun {
		label = "would write"
		verb = "would " + verb
	}
	return view.KeyValue{Pairs: slices.Concat([]view.Pair{
		{Key: label, Value: verb + " in " + config.Path()},
		{Key: "back", Value: configBack(key, before, had)},
	}, outputOutranked(key, strings.Join(configValueLines(after), "")))}, nil
}

// outputOutranked says so when the key just written is the output format and
// $RTA_OUTPUT is exported, which outranks the file: the next command prints in
// the variable's format, and a receipt that only said "set output to json" would
// leave the person looking for the reason in the file they just edited.
func outputOutranked(key configKey, now string) []view.Pair {
	env := os.Getenv("RTA_OUTPUT")
	if now == "" {
		now = "pretty"
	}
	if key.Name != "output" || env == "" || env == now {
		return nil
	}
	return []view.Pair{{Key: "note", Value: "RTA_OUTPUT=" + env +
		" is exported in this shell and outranks the file, so commands keep printing " + env +
		" until it is unset"}}
}

// configStated is what a key is, for a sentence: its value, or what it is while
// the file says nothing.
func configStated(key configKey, v any, has bool) string {
	if has {
		return "is already " + strings.Join(configValueLines(v), ", ")
	}
	def := key.Default
	if def == "" {
		def = "unset"
	}
	return "is " + def
}

// configBack is the command that undoes a write, which is the receipt's last
// word: the old value if there was one, and a removal if there was none.
func configBack(key configKey, before any, had bool) string {
	if !had {
		return "`rta config unset " + key.Name + "` returns it to " + defaultWords(key)
	}
	args := []string{"rta", "config", "set", key.Name}
	for _, line := range configValueLines(before) {
		args = append(args, shellquote.Arg(line))
	}
	return "`" + strings.Join(args, " ") + "` puts it back"
}

func defaultWords(key configKey) string {
	if key.Default == "" {
		return "its default"
	}
	return "its default, " + key.Default
}

// runConfigUnset is `rta config unset`.
func runConfigUnset(reg *registry.Registry, raw string, dryRun bool) (view.View, *view.Error) {
	key, verr := resolveConfigKey(reg, raw)
	if verr != nil {
		return nil, verr
	}
	if verr := refuseUnhonouredKey(key); verr != nil {
		return nil, verr
	}
	var before any
	var had, removed bool
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		before, had = key.read(cfg)
		removed = key.drop(&cfg)
		return cfg, removed && !dryRun
	}); err != nil {
		return nil, view.AsError(err, "core.config.write")
	}
	if !removed {
		return view.KeyValue{Pairs: []view.Pair{{Key: "unchanged",
			Value: key.Name + " is not set — nothing written to " + config.Path()}}}, nil
	}
	label, verb := "wrote", "removed "+key.Name+" from "+config.Path()
	if dryRun {
		label, verb = "would write", "would remove "+key.Name+" from "+config.Path()
	}
	pairs := []view.Pair{{Key: label, Value: verb + ", so it is back to " + defaultWords(key)}}
	if had {
		pairs = append(pairs, view.Pair{Key: "back", Value: configBack(key, before, true)})
	}
	return view.KeyValue{Pairs: append(pairs, outputOutranked(key, "")...)}, nil
}
