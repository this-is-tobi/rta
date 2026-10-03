package app

import (
	"regexp"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// capabilityName is a backticked token spelled like a capability or a group of
// them: dotted lower-case words, with `a.b.c/d` as the shorthand for two.
var capabilityName = regexp.MustCompile("`([a-z][a-z0-9-]*(?:\\.[a-z][a-z0-9-]*)+(?:/[a-z][a-z0-9-]*)*)`")

// TestNoDescriptionNamesACapabilityThatDoesNotExist walks the built-in
// catalogue and refuses a backticked capability name that nothing declares.
//
// keys.backup told every reader of `rta explain keys.backup` that it was
// classified as it was "for the same reason `share.secret.set/get` will" — two
// capabilities of a plugin that was planned, never written, and never
// shipped. A description is read as a statement about the tool in front of
// you; one that cites a neighbour that is not there sends the reader to look
// for it, and nothing but a walk of the whole catalogue notices when a name in
// a sentence stops being true. A name is accepted as a capability, or as the
// start of one (`net.hosts` holds four).
func TestNoDescriptionNamesACapabilityThatDoesNotExist(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, c := range reg.Capabilities() {
		known[c.ID] = true
		parts := strings.Split(c.ID, ".")
		for i := 1; i < len(parts); i++ {
			known[strings.Join(parts[:i], ".")] = true
		}
	}
	for _, c := range reg.Capabilities() {
		texts := []string{c.Summary, c.Description}
		for _, f := range c.Inputs {
			texts = append(texts, f.Help)
		}
		for _, text := range texts {
			for _, m := range capabilityName.FindAllStringSubmatch(text, -1) {
				head, alternatives, _ := strings.Cut(m[1], "/")
				names := []string{head}
				if alternatives != "" {
					stem := head[:strings.LastIndex(head, ".")+1]
					for _, alt := range strings.Split(alternatives, "/") {
						names = append(names, stem+alt)
					}
				}
				for _, name := range names {
					if !known[name] {
						t.Errorf("%s names `%s`, which no capability declares", c.ID, name)
					}
				}
			}
		}
	}
}

// TestNoInputHelpRepeatsWhatTheHostAlreadyPrints walks the built-in
// catalogue and refuses a Help string that names its own environment
// variable.
//
// `rta explain` prints ", from $RTA_KV_PASSPHRASE" from EnvFallback
// (explain.go), and flagUsage appends the same variable to --help. Three
// declarations wrote it into Help as well, one of them by calling
// LocalEnvVar itself, so the explain card named the variable twice in one
// line. There is no way to notice that by reading a declaration — the
// duplicate is two hundred lines away in another package — which is what
// makes it a drift test rather than a review note.
func TestNoInputHelpRepeatsWhatTheHostAlreadyPrints(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range reg.Capabilities() {
		for _, f := range c.Inputs {
			if !f.EnvFallback {
				continue
			}
			if env := plugin.LocalEnvVar(c.ID, f.Name); strings.Contains(f.Help, env) {
				t.Errorf("%s input %q names %s in its Help; the host already prints it "+
					"from EnvFallback — in `rta explain` and in --help — so the card says it twice. "+
					"State what the value is and let the surface say where it comes from.",
					c.ID, f.Name, env)
			}
		}
	}
}

// The environment variable a credential may arrive in is the host's to name
// on --help, generated from the declaration the way `rta explain` already
// generates its ", from $RTA_KV_PASSPHRASE" clause.
//
// Before this, --help named the variable only where a declaration had written
// it into Help by hand: three built-ins did, in a spelling of their own, and
// no plugin credential ever had — so `rta s3 object get --help` never said
// how to supply the secret key without putting it on argv. kv.rm is the
// fixture because it is a built-in whose store reads a credential from the
// environment, so the test needs no plugin process, and the spelling asserted
// is the one explain uses, which no declaration writes.
func TestCLIHelpNamesTheEnvironmentVariableForACredential(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	cmd, _, err := root.Find([]string{"kv", "rm"})
	if err != nil {
		t.Fatal(err)
	}
	flag := cmd.Flags().Lookup("passphrase")
	if flag == nil {
		t.Fatal("kv rm has no --passphrase, so this test checks nothing")
	}
	if !strings.Contains(flag.Usage, "$RTA_KV_PASSPHRASE") {
		t.Errorf("--passphrase usage does not name $RTA_KV_PASSPHRASE: %q", flag.Usage)
	}
}

// A Piped input is read from standard input when the CLI leaves it out, and
// --help says so from the declaration. The descriptions of codec.jwt,
// codec.jwk and debug.ansi used to end on a sentence saying it, which every
// agent was sent as well, for a pipe it has none of.
func TestCLIHelpSaysAPipedInputIsReadFromStandardInput(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"codec.jwt", "codec.jwk", "debug.ansi"} {
		c, ok := reg.Capability(id)
		if !ok {
			t.Fatalf("no capability %s", id)
		}
		var piped plugin.Field
		for _, f := range c.Inputs {
			if f.Piped {
				piped = f
			}
		}
		if piped.Name == "" {
			t.Fatalf("%s has no piped input, so this test checks nothing", id)
		}
		if got := flagUsage(c, piped); !strings.Contains(got, "read from standard input when left out") {
			t.Errorf("%s --help says %q of %s, and not that it is read from standard input", id, got, piped.Name)
		}
		if strings.Contains(c.Description, "pipe") {
			t.Errorf("%s: the description talks about a pipe the help already mentions: %q", id, c.Description)
		}
	}
}
