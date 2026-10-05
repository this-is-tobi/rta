package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/this-is-tobi/rta/builtin/audit"
	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/pkg/view"
)

// What `rta init` finds to offer, decided from the machine and nothing else.

// initOffer is one registration init can make: which client, and the command
// that makes it, shown before anybody is asked.
type initOffer struct {
	client mcpClient
	req    installRequest
	line   string
	scope  string
}

func (o initOffer) question() string { return "Register rta with " + o.client.label + "?" }

// explain is where it registers and the command it runs: the answer to "what
// does yes do", on the page that asks.
func (o initOffer) explain() string {
	return o.scope + "\n$ " + o.line + "\nRead-only until you grant more (`rta grant allow`)."
}

// later is the command that makes this registration by hand.
func (o initOffer) later() string {
	line := "rta mcp install " + o.client.name
	if o.req.global && o.client.globalArgs != nil {
		line += " --global"
	}
	return line
}

// initPlan is what init found: the registrations it can offer, the clients that
// already have one, and what is left to the person to do by hand.
type initPlan struct {
	offers  []initOffer
	already []string
	notes   []view.Pair
}

// planInit looks at the machine: which clients are on it and whether rta is
// registered with them, how the shell's completion stands, and whether a plugin
// index is attached.
//
// A client that already holds a registration for rta, in any scope and with any
// options, is left alone. Registering over it would take out whatever the
// operator put there — a --consent, a --root — to put in the plain one, and
// "set rta up" is never a reason to undo a choice that was made on purpose.
func planInit(ctx context.Context) initPlan {
	home, _ := os.UserHomeDir()
	wd, _ := os.Getwd()
	var plan initPlan
	for _, seen := range seenClients(home) {
		if !seen.present() {
			continue
		}
		c := seen.client
		if registeredWith(c, home, wd) {
			plan.already = append(plan.already, c.label)
			continue
		}
		if c.bin == "" || !seen.cli {
			plan.notes = append(plan.notes, view.Pair{Key: c.name, Value: c.label + " is here and rta runs " +
				"nothing for it — `rta mcp install " + c.name + "` prints what to add to " + c.file})
			continue
		}
		// For every project where the client has a flag for that: this is a
		// person setting a machine up, and a registration that holds only in the
		// directory they happened to run init from is not what they asked for.
		req := installRequest{client: c, as: c.name, global: c.globalArgs != nil || c.alwaysGlobal, dryRun: true}
		shown, err := installClient(ctx, io.Discard, req)
		if err != nil || shown.block {
			plan.notes = append(plan.notes, view.Pair{Key: c.name, Value: "`rta mcp install " + c.name +
				"` says what is in the way of registering it with " + c.label})
			continue
		}
		plan.offers = append(plan.offers, initOffer{client: c, req: req,
			line: answerValue(shown.answer, "would run"), scope: answerValue(shown.answer, "scope")})
	}
	if note, ok := completionNote(filepath.Base(os.Getenv("SHELL")), home); ok {
		plan.notes = append(plan.notes, note)
	}
	if note, ok := pluginsNote(); ok {
		plan.notes = append(plan.notes, note)
	}
	return plan
}

// registeredWith says whether rta is already registered with a client, by the
// same reading `rta doctor` and `rta audit clients` do.
func registeredWith(c mcpClient, home, wd string) bool {
	if c.name == "claude" {
		return len(claudeRegistrations(home, wd)) > 0
	}
	found, _ := audit.Registrations(home, wd)
	for _, r := range found {
		if strings.HasPrefix(r.Label, c.auditLabel) {
			return true
		}
	}
	return false
}

// answerValue is the value of a key in an answer, or "".
func answerValue(kv view.KeyValue, key string) string {
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

// completionNote is the line that turns on tab completion for the shell, or
// false when the shell is one rta has no recipe for or the script is already
// where the shell loads it from. The recipes are the installation page's, and
// they are printed and not run: they end in a shell's own startup file or a
// directory it reads on start, and rta does not write another tool's files.
func completionNote(shell, home string) (view.Pair, bool) {
	var installed []string
	var how string
	switch shell {
	case "zsh":
		installed = []string{
			filepath.Join(home, ".zsh", "completions", "_rta"),
			"/opt/homebrew/share/zsh/site-functions/_rta", "/usr/local/share/zsh/site-functions/_rta",
		}
		how = "`mkdir -p ~/.zsh/completions && rta completion zsh > ~/.zsh/completions/_rta`, then once " +
			"`fpath=(~/.zsh/completions $fpath)` in ~/.zshrc above compinit"
	case "bash":
		installed = []string{filepath.Join(home, ".local", "share", "bash-completion", "completions", "rta")}
		how = "`mkdir -p ~/.local/share/bash-completion/completions && " +
			"rta completion bash > ~/.local/share/bash-completion/completions/rta`"
	case "fish":
		installed = []string{filepath.Join(home, ".config", "fish", "completions", "rta.fish")}
		how = "`rta completion fish > ~/.config/fish/completions/rta.fish`"
	default:
		return view.Pair{}, false
	}
	for _, p := range installed {
		if _, err := os.Stat(p); err == nil {
			return view.Pair{}, false
		}
	}
	return view.Pair{Key: "completion", Value: shell + " is not set up — " + how + "; then restart the shell"}, true
}

// pluginsNote is the command that attaches the first-party plugin index, when
// no index is attached. The databases, object storage, secrets and Kubernetes
// tools are plugins and invisible until one is.
func pluginsNote() (view.Pair, bool) {
	if len(plugindist.Indexes()) > 0 {
		return view.Pair{}, false
	}
	url, _ := plugindist.KnownIndexURL("official")
	return view.Pair{Key: "plugins", Value: "databases, object storage, secrets and Kubernetes are plugins and none is " +
		"installed — `rta plugin index add official` attaches the first-party index (" + url + "), then " +
		"`rta plugin install <name>`"}, true
}
