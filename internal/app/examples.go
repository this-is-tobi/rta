package app

import (
	"strings"

	"github.com/spf13/cobra"
)

// examples are what `rta <command> --help` shows under EXAMPLES, for the
// commands a person types every day. Of the thirty-eight help screens somebody
// walked through to find the first usable command line, one had an example; the
// rest began with a paragraph, and the line to copy was in the documentation.
//
// A map keyed by the command's path, in a file of its own, rather than a field
// set where each command is built: the commands are assembled across a dozen
// files and the capability ones come out of a registry, so a reader checking
// that a command's example uses the flags it really has would be reading
// thirty places. Here they sit side by side, and the tests parse every line
// against the real tree — a flag renamed under an example fails the build, not
// the person who copied it.
//
// A line starting with # is a comment and is drawn dimmed; every other line is
// a command that runs as written, so a value is a believable one
// (`db-password`, `claude`), never a <placeholder>. Two to four lines each: one
// that stands alone and the flag people reach for second. A command that
// declares its own Example in the place it is built keeps it, and is not listed
// here — the tests refuse both.
var examples = map[string][]string{
	"rta grant allow": {
		"rta grant allow net.dns example.org --agent claude --ttl 30m",
		"rta grant allow kv.get db-password --agent claude --ttl 15m --max-uses 1",
		"rta grant allow pg.query --profile staging --agent claude --ttl 1h",
	},
	"rta grant revoke": {
		"rta grant revoke kv.get db-password   # take one grant back now",
		"rta grant revoke kv --agent claude    # everything claude holds on kv",
		"rta grant revoke --all",
	},
	"rta grant issue": {
		"rta grant issue dev --agent claude   # every line of the role at once",
		"rta grant issue dev --agent claude --ttl 4h",
	},
	"rta grant list": {
		"rta grant list               # what is allowed right now",
		"rta grant list --detail      # and what needs no grant at all",
		"rta grant list --role dev    # what stands under one role",
	},
	"rta grant renew": {
		"rta grant renew                  # every active grant, for as long again",
		"rta grant renew kv.get db-password --ttl 30m",
		"rta grant renew --role dev",
	},
	"rta grant roles": {
		"rta grant roles",
		"rta grant roles dev   # one role, every line of it",
	},
	"rta lock": {
		"rta lock claude --note \"runaway loop\"   # the same as rta lock add",
	},
	"rta lock add": {
		"rta lock add claude --note \"runaway loop, ping me\"",
		"rta lock add claude --ttl 30m      # lifts itself",
		"rta lock add dash --kind operator --server work",
	},
	"rta lock rm": {
		"rta lock rm claude",
		"rta lock rm dash --kind operator --server work",
	},
	"rta agent log": {
		"rta agent log               # one line per call, the latest at the bottom",
		"rta agent log --refused     # only what rta would not do",
		"rta agent log --since 2h --limit 50",
		"rta agent log --detail      # and whether the chain still verifies",
	},
	"rta agent allow": {
		"rta agent allow 5473aa62             # this call, and nothing wider",
		"rta agent allow 5473aa62 --ttl 15m   # and the same question for 15m",
	},
	"rta agent show": {
		"rta agent show 5473aa62   # everything about it, and what it would do",
	},
	"rta use": {
		"rta use staging",
		"rta use staging --for 2h   # falls back to the base configuration on its own",
		"rta use                    # what is on",
		"rta use --off",
	},
	"rta kv set": {
		"rta kv set db-password                  # asks for it, echoing nothing",
		"rta kv set tls-cert --file server.pem   # in no argv, no shell history",
		"rta kv set db-password --description \"staging replica\"",
	},
	"rta kv init": {
		"rta kv init --generate                    # a dedicated key, no passphrase",
		"rta kv init --identity ~/.ssh/id_ed25519  # a key you already have",
		"rta kv init --recipient \"$(cat alice.pub)\"  # let somebody else read it too",
	},
	"rta plugin install": {
		"rta plugin install pg",
		"rta plugin install official/pg   # when more than one index claims the name",
	},
	"rta plugin trust": {
		"rta plugin trust           # what was found and has not run",
		"rta plugin trust weather   # approve that build",
	},
	"rta mcp install": {
		"rta mcp install claude",
		"rta mcp install claude --global           # every project, not this one",
		"rta mcp install cursor --as cursor-work   # its own name, grants apart",
		"rta mcp install vscode --show             # print what to add, nothing else",
	},
	"rta mcp serve": {
		"rta mcp serve --as claude",
		"rta mcp serve --as claude --consent        # park a call that needs a grant",
		"rta mcp serve --as claude --root ~/work    # paths may name this directory",
	},
	"rta policy init": {
		"rta policy init         # a commented .rta-policy.yaml, to commit",
		"rta policy init --force",
	},
	"rta doctor": {
		"rta doctor",
		"rta doctor -o json",
	},
	"rta init": {
		"rta init",
	},
	"rta explain": {
		"rta explain            # every capability, and what it needs",
		"rta explain sys.cpu    # one, with the line to type and the grant it needs",
		"rta explain kv         # everything under kv",
	},
}

// attachExamples gives each command in the map its examples, over the finished
// tree, for the reason describeGroups is a pass: the commands are built in many
// places and none of them knows this table exists.
func attachExamples(cmd *cobra.Command) {
	if lines, ok := examples[cmd.CommandPath()]; ok && cmd.Example == "" {
		cmd.Example = strings.Join(lines, "\n")
	}
	for _, sub := range cmd.Commands() {
		attachExamples(sub)
	}
}
