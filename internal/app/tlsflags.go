package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The three ways a command line gets TLS wrong that the flag parser answers
// with a sentence about something else.
//
// TLS is where the plugins disagree most about what the switch is called: etcd
// takes a `--tls` switch, mysql and mariadb a `--tls` that takes one of five
// words, postgres `--sslmode`, and vault and keycloak no switch at all, the
// scheme of the address they are given being what decides. A person who knows
// one reaches for it on another, and each of the three mistakes below was
// answered with the parser's own report: a guess at a neighbouring flag that does
// nothing without TLS, an argument it has no place for, and the same again
// after a flag swallowed the next one.

// tlsWords are the names a person gives the switch that turns TLS on, and that a
// capability may not have under any of them.
var tlsWords = []string{"tls", "ssl", "https"}

// tlsFlagHint is what to say to `--tls`, `--ssl` or `--https` on a capability
// that has no such flag: the input that says TLS in its own words, which is the
// one that declares the TLS endpoint role (plugin.EndpointTLS), or, for a
// capability whose address carries the scheme, that `https://` is what turns it
// on. "" for any other flag or a capability with neither, which keeps the guess
// at the nearest spelling.
//
// The guess was the problem. `rta pg status --tls` was answered "did you mean
// --tls-server-name?", the name of the input that says what to call the server
// once TLS is on and does nothing before.
func tlsFlagHint(c plugin.Capability, typed string) string {
	if !slices.Contains(tlsWords, strings.ToLower(typed)) {
		return ""
	}
	var address string
	for _, f := range c.Inputs {
		switch f.Endpoint {
		case plugin.EndpointTLS:
			hint := "--" + f.Name + " turns TLS on"
			if len(f.Options) > 0 {
				hint += " — it takes one of " + strings.Join(f.Options, ", ")
			}
			return hint
		case plugin.EndpointURL:
			if address == "" {
				address = f.Name
			}
		}
	}
	if address != "" {
		return "there is no TLS switch: an https:// address in --" + address + " is what turns it on"
	}
	return ""
}

// switchValueHint answers a word the command has no place for that reads as the
// value of a switch given before it: `rta etcd overview --tls true`. A switch is
// on by being named, so the word is left over and refused as an unexpected
// argument, under a hint that lists the flags that do take a value.
//
// Said for the switches that were given, since the parser does not keep the
// order they were given in and so cannot say which one the word followed; with
// one given it names it, and with several it names them all. "" when no leftover
// word reads as a value, or no switch was given.
func switchValueHint(cmd *cobra.Command) string {
	valued := false
	for _, word := range cmd.Flags().Args() {
		switch strings.ToLower(word) {
		case "true", "false", "yes", "no", "on", "off", "1", "0":
			valued = true
		}
	}
	if !valued {
		return ""
	}
	var switches []string
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if f.Value.Type() == "bool" && !f.Hidden && f.Name != "help" {
			switches = append(switches, f.Name)
		}
	})
	switch len(switches) {
	case 0:
		return ""
	case 1:
		return "a switch needs no value: `--" + switches[0] + "` alone turns it on, `--" + switches[0] + "=false` off"
	}
	for i, name := range switches {
		switches[i] = "--" + name
	}
	return strings.Join(switches, ", ") + " are switches, which need no value: `" + switches[0] +
		"` alone turns it on, `" + switches[0] + "=false` off"
}

// refuseSwallowedFlag refuses a flag that takes a value when the value it was
// given is the spelling of another of the command's flags: `rta mysql status
// --tls --host db` reads `--host` as the answer to `--tls` and then finds `db`
// with no flag to belong to. The refusal that followed was "unexpected argument
// db" about the word that was right, and with nothing after the second flag
// there was no refusal at all, only a connection attempt with a TLS mode called
// "--host".
//
// It says what happened in the order it did: the flag needs a value, the next
// word looked like a flag, and, when the input declares its answers, which they
// are. A value that is the name of a flag of the same command is a mistake and
// never an answer, so there is no spelling to ask for it on purpose.
func refuseSwallowedFlag(cmd *cobra.Command, c plugin.Capability) error {
	var refused error
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if refused != nil || f.Value.Type() != "string" {
			return
		}
		word := f.Value.String()
		name, dashed := strings.CutPrefix(word, "--")
		name, _, _ = strings.Cut(name, "=")
		if !dashed || name == f.Name || cmd.Flags().Lookup(name) == nil {
			return
		}
		hint := "give --" + f.Name + " its value first, as in `" + cmd.CommandPath() + " --" + f.Name + " <value> " + word + "`"
		for _, in := range c.Inputs {
			if in.Name == f.Name && len(in.Options) > 0 {
				hint = "--" + f.Name + " takes one of " + strings.Join(in.Options, ", ") + " — put it right after --" + f.Name
			}
		}
		refused = &view.Error{Code: CodeUsage,
			Message: fmt.Sprintf("--%s needs a value, and the next word, %s, looks like a flag", f.Name, word),
			Hint:    hint}
	})
	return refused
}
