package plugin

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Registration-time validation: the closed sets a declaration may draw
// from, and every rule a Plugin must satisfy before the registry takes it.
// A plugin that validates is one whose declared surface — inputs, config
// keys, endpoint roles, bounds — can be trusted by every host that reads
// it, which is why the rules live beside the contract they police.

var (
	idRe    = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*){1,2}$`)
	nameRe  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	fieldRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// A dotted path of the same segments, so a plugin can nest its own
	// settings. Closed deliberately: no leading dot, no empty segment, no
	// "..", nothing that could be read as a filesystem path by whatever
	// looks at it next.
	configKeyRe = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)*$`)
	safetySet   = map[Safety]bool{Read: true, Write: true, Destructive: true}
	// safeties is safetySet in the order harm increases, which is the order
	// anything listing them should use.
	safeties = []Safety{Read, Write, Destructive}
	// fieldTypes is the closed set an input may declare, in the order the
	// rejection message lists them.
	fieldTypes = []FieldType{String, Int, Bool, Float, StringSlice, Text, Path, Secret}
)

// FieldTypes returns every type an input may declare, in the order the
// rejection message lists them.
//
// The set is closed and the zero value is not a member, so anything
// that maps a declaration onto another representation — a wire enum, a JSON
// Schema, a form widget, a code generator — has a finite set to cover and a
// way to find out when it grows. Exported for exactly that: the alternative is
// every such mapping restating the list from memory, which is how one of them
// ends up missing Secret and rendering a credential as a plain string field.
func FieldTypes() []FieldType { return slices.Clone(fieldTypes) }

// Safeties returns every safety class, in the order harm increases.
func Safeties() []Safety { return slices.Clone(safeties) }

// ValidName reports whether s is a legal plugin namespace. Exported so that
// anything holding a namespace that did not come through Validate — an index
// manifest's claim, before any binary exists to validate — is held to the
// same grammar rather than a restated one: a list has one home.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// ValidID reports whether s is a legal capability ID, for the same reason.
func ValidID(s string) bool { return idRe.MatchString(s) }

// ValidSafety reports whether s names a safety class.
func ValidSafety(s string) bool { return safetySet[Safety(s)] }

// fieldTypeList renders the accepted types for an error message, so a rejected
// plugin is told what to write instead of what not to.
func fieldTypeList() string {
	names := make([]string, len(fieldTypes))
	for i, t := range fieldTypes {
		names[i] = string(t)
	}
	return strings.Join(names, ", ")
}

// Validate checks structural correctness of a plugin declaration. It is used
// by the registry at load time and by the sdktest conformance suite.
func (p Plugin) Validate() error {
	if !nameRe.MatchString(p.Name) {
		return fmt.Errorf("plugin name %q: must be lowercase [a-z0-9-]", p.Name)
	}
	if why, reserved := reservedNamespaces[p.Name]; reserved {
		return fmt.Errorf("plugin name %q is reserved by the host (%s); pick another namespace",
			p.Name, why)
	}
	if len(p.Capabilities) == 0 {
		return fmt.Errorf("plugin %q declares no capabilities", p.Name)
	}
	// Name is already constrained to [a-z0-9-] by nameRe; the prose is not.
	if err := checkLine(fmt.Sprintf("plugin %q summary", p.Name), p.Summary, maxSummary); err != nil {
		return err
	}
	if err := checkLine(fmt.Sprintf("plugin %q version", p.Name), p.Version, maxOption); err != nil {
		return err
	}
	// A need rta does not know is refused rather than ignored: silently
	// dropping it would leave a plugin declaring a requirement no surface can
	// show and no operator can grant, which reads as "asked for and denied"
	// and is really "never asked".
	// Deduplicated for the same reason capability IDs are, one loop down: the
	// set is what every surface counts, prints and grants against. A repeat
	// makes `rta plugin allow` offer one location twice and makes an index's
	// claim about it unrepresentable — a manifest refuses a duplicate — so the
	// plugin that would be unpublishable is refused here, where its author is,
	// rather than later where somebody else is.
	askedFor := map[Need]bool{}
	for _, n := range p.Needs {
		if !KnownNeed(n) {
			return fmt.Errorf("plugin %q: unknown need %q; rta knows %s",
				p.Name, n, needList())
		}
		if askedFor[n] {
			return fmt.Errorf("plugin %q: duplicate need %q", p.Name, n)
		}
		askedFor[n] = true
	}
	seen := map[string]bool{}
	for _, c := range p.Capabilities {
		if err := c.validate(p.Name); err != nil {
			return err
		}
		if seen[c.ID] {
			return fmt.Errorf("duplicate capability ID %q", c.ID)
		}
		seen[c.ID] = true
	}
	return nil
}

// needList renders the accepted needs for an error message, so a rejected
// plugin is told what to write rather than only what not to.
func needList() string {
	names := make([]string, len(needs))
	for i, n := range needs {
		names[i] = string(n)
	}
	return strings.Join(names, ", ")
}

func (c Capability) validate(ns string) error {
	if !idRe.MatchString(c.ID) {
		return fmt.Errorf("capability ID %q: want 2-3 lowercase dot-separated segments", c.ID)
	}
	if !strings.HasPrefix(c.ID, ns+".") {
		return fmt.Errorf("capability %q: ID must start with plugin namespace %q", c.ID, ns)
	}
	if c.Summary == "" {
		return fmt.Errorf("capability %q: summary is required", c.ID)
	}
	if err := checkLine(fmt.Sprintf("capability %q: summary", c.ID), c.Summary, maxSummary); err != nil {
		return err
	}
	if err := checkText(fmt.Sprintf("capability %q: description", c.ID), c.Description, maxDescription); err != nil {
		return err
	}
	if !safetySet[c.Safety] {
		return fmt.Errorf("capability %q: invalid safety %q", c.ID, c.Safety)
	}
	if c.Run == nil {
		return fmt.Errorf("capability %q: nil handler", c.ID)
	}
	scoped := c.Scope == ""
	seenInputs := map[string]bool{}
	for _, f := range c.Inputs {
		if !fieldRe.MatchString(f.Name) {
			return fmt.Errorf("capability %q: field name %q must be lowercase [a-z0-9-]", c.ID, f.Name)
		}
		// Two fields sharing a name is not a harmless typo: declareFlags
		// registers one pflag.Flag per input in declaration order, and the
		// second AddFlag for the same name panics the whole process at
		// startup — not just for the capability that declared it, since
		// every registered plugin's flags are built into one command tree
		// before any command runs. The same duplicate-check Plugin.Validate
		// already does one level up, for capability IDs.
		if seenInputs[f.Name] {
			return fmt.Errorf("capability %q declares input %q twice", c.ID, f.Name)
		}
		seenInputs[f.Name] = true
		if why, reserved := reservedInputs[f.Name]; reserved {
			return fmt.Errorf("capability %q: input %q is reserved by the host (%s); rename it",
				c.ID, f.Name, why)
		}
		// "profile" is reserved only where the host would actually own it.
		//
		// Unlike every name in reservedInputs, this one is conditional, and the
		// condition is the hazard itself: the host declares --profile exactly
		// when Profilable(c) — a capability with an input a profile could fill
		// — so that is exactly the set where a declared "profile" would be
		// shadowed by the host's flag and silently never reach the handler.
		//
		// Where a capability has nothing a profile could fill, no flag is
		// added, nothing is stripped from an MCP call, and the name is
		// ordinary. builtin/grant is the case that proves it has to work this
		// way: `rta grant allow pg --profile staging` takes a profile name as
		// *data* — the thing being granted — and an unconditional reservation
		// would make the command that issues profile grants the one command
		// unable to name a profile.
		if f.Name == "profile" && profilable(c.Inputs, c.Scope) {
			return fmt.Errorf("capability %q: input %q is reserved by the host on any capability "+
				"a profile can fill (the host resolves which configured connection a call runs "+
				"against, before the grant gate); rename it", c.ID, f.Name)
		}
		if f.Endpoint != EndpointNone {
			if !slices.Contains(endpointRoles, f.Endpoint) {
				return fmt.Errorf("capability %q: input %q declares endpoint role %q, which the host "+
					"does not recognise; the roles are host, port, address, url and tls",
					c.ID, f.Name, f.Endpoint)
			}
			// Refused for the reason Config is, one step along. A tunnel fills
			// this with an address it computed; an input that takes a
			// credential taking one instead is a connection pointed somewhere
			// nobody chose, and an author reaching for it has confused the
			// address with what authenticates to it.
			if f.Type == Secret {
				return fmt.Errorf("capability %q: input %q is a Secret and cannot take an endpoint role; "+
					"a tunnel fills an address, not a credential — the credential beside it comes from "+
					"the profile's `secrets:` mapping", c.ID, f.Name)
			}
			// A tunnel is a destination, and a destination is a destination
			// whether or not it is on this machine. An
			// endpoint input that a caller could supply would let an agent
			// name the address the operator's credential is carried to, which
			// is the hole Local exists to close.
			if !f.Local {
				return fmt.Errorf("capability %q: input %q takes an endpoint role without Local; "+
					"an input that decides where a call goes must not be settable by a remote caller, "+
					"or an agent can point the operator's credential at a machine it chose", c.ID, f.Name)
			}
			// **Config is what keeps this from being a way to widen reach.**
			// pkg/plugin/profile.go's rule is that nothing a *plugin* declares
			// may decide what a profile fills, because a plugin that could
			// mark its own inputs profile-fillable could mark the one naming a
			// file. Endpoint is the first declared thing the host acts on, so
			// it is confined to inputs the author had already opened to the
			// operator: a tunnel fills the same slot a config file fills, with
			// a value the host computed instead of one it read. An input
			// nobody said an operator could set is not one a tunnel may reach,
			// and ProfileFillable — which refuses a Path and refuses the Scope
			// input — is the same gate the fill itself applies.
			if f.Config == "" {
				return fmt.Errorf("capability %q: input %q takes an endpoint role without a Config key; "+
					"a tunnel fills what configuration fills, with a value the host computed rather than "+
					"read, so an input an operator was never offered is not one it may reach", c.ID, f.Name)
			}
			if f.Endpoint == EndpointPort && f.Type != Int {
				return fmt.Errorf("capability %q: input %q takes the port role and is %s; "+
					"a port is an Int", c.ID, f.Name, f.Type)
			}
			// The TLS role is the one where the host has to produce a value
			// the *plugin* understands rather than one the host defines, so
			// what it can say has to be pinned at registration. A Bool is
			// unambiguous. A String is whatever the author's library spells
			// "off", so it has to enumerate its Options and one of them has to
			// be a spelling the host knows — otherwise the host would fill
			// `sslmode` with a word the plugin rejects, and the failure is an
			// argument error on a call the operator did not know was tunnelled.
			if f.Endpoint == EndpointTLS {
				switch {
				case f.Type == Bool:
				case f.Type == String && tlsOffValue(f) != "":
				case f.Type == String:
					return fmt.Errorf("capability %q: input %q takes the tls role and is a String whose "+
						"Options name no way to turn TLS off; declare Options including one of %s, or "+
						"make it a Bool", c.ID, f.Name, strings.Join(tlsOff, ", "))
				default:
					return fmt.Errorf("capability %q: input %q takes the tls role and is %s; "+
						"the host fills it with a Bool or with one of %s, so it must be one of those two "+
						"types", c.ID, f.Name, f.Type, strings.Join(tlsOff, ", "))
				}
			}
		}
		if f.Config != "" {
			if !configKeyRe.MatchString(f.Config) {
				return fmt.Errorf("capability %q: input %q has config key %q; want dot-separated lowercase segments",
					c.ID, f.Name, f.Config)
			}
			// Refused rather than quietly ignored, and the message names the
			// alternative, because an author who reaches for this is trying
			// to solve a real problem and needs to be pointed at the path
			// that already solves it.
			if f.Type == Secret {
				return fmt.Errorf("capability %q: input %q is a Secret and cannot be filled from config; "+
					"config is a plaintext file read on every invocation and a Secret default is published "+
					"in the MCP tool schema — declare Local: true and let the host resolve it from its own "+
					"environment instead", c.ID, f.Name)
			}
			if f.Positional {
				return fmt.Errorf("capability %q: input %q is positional and cannot be filled from config; "+
					"CLI argument arity is computed from Required, so a config-satisfied positional changes "+
					"what an argument count means", c.ID, f.Name)
			}
		}
		// A completion list is drawn in plain text next to the box it
		// completes, so the two types whose whole point is that their value is
		// not shown like that may not declare one.
		//
		// Refused rather than dropped by whichever surface happens to render
		// it. It used to be honoured by the shell and by `rta explain`, which
		// prints ", completes" from the declaration, and silently ignored by
		// the TUI — so an author reading the docs was told three different
		// things about the same field. A Secret's list would defeat the mask it
		// is drawn beside; a Text field is a body written in an editor, and huh
		// offers no completion for one at all.
		if f.Suggest != nil && (f.Type == Secret || f.Type == Text) {
			return fmt.Errorf("capability %q: input %q is a %s and cannot declare Suggest; "+
				"a completion list is rendered in plain text beside the field, which defeats a "+
				"Secret's mask, and a Text body is written in an editor rather than completed",
				c.ID, f.Name, f.Type)
		}
		if f.EnvFallback && !f.Local {
			return fmt.Errorf("capability %q: input %q declares EnvFallback without Local; "+
				"EnvFallback only changes how a Local field resolves, and a non-Local field is already "+
				"reachable from a caller directly", c.ID, f.Name)
		}
		// Live is the deliberate-press completion channel (Field.Live), and
		// these rules keep it coherent: it marks a Suggest, so one must
		// exist; a closed set is already the whole answer (Options wins over
		// Suggest in Candidates, so a live one would never run); and live
		// completion types into a String box — the other types have their
		// own machinery, and widening this is a decision for the field that
		// needs it, not a default.
		if f.Live {
			if f.Suggest == nil {
				return fmt.Errorf("capability %q: input %q declares Live with no Suggest to run",
					c.ID, f.Name)
			}
			if len(f.Options) > 0 {
				return fmt.Errorf("capability %q: input %q declares Live beside Options; "+
					"a closed set is already the whole answer", c.ID, f.Name)
			}
			if f.Type != String {
				return fmt.Errorf("capability %q: input %q declares Live but is a %s; "+
					"live completion types into a String box", c.ID, f.Name, f.Type)
			}
		}
		// Every surface switches on Field.Type with a default branch meaning
		// "string", so a type nothing recognises is caught nowhere downstream.
		// Type: "integer" — JSON Schema's spelling, and the obvious thing to
		// reach for — builds a --port string flag, publishes {"type": "string"}
		// in the MCP schema, and hands the handler a value req.Int reads as 0.
		// No error anywhere: the capability just runs against port 0.
		//
		// It has to be rejected here rather than added later. Once pkg/plugin
		// is semver-committed, turning the silent string default into an error
		// breaks every plugin that had come to depend on it.
		//
		// The zero value is rejected too, and that is a deliberate second
		// decision rather than a consequence of the first. Field{Name: "host"}
		// used to validate and behave as a string everywhere, which is exactly
		// how a credential input ends up untyped: Secret is what makes a value
		// masked, Path is what makes it completable, and neither is something
		// the host can infer from a name. Making the author say which one it
		// is costs a word and is the only moment the question gets asked.
		switch {
		case f.Type == "":
			return fmt.Errorf("capability %q: input %q declares no type, want one of %s",
				c.ID, f.Name, fieldTypeList())
		case !slices.Contains(fieldTypes, f.Type):
			return fmt.Errorf("capability %q: input %q has unknown type %q, want one of %s",
				c.ID, f.Name, f.Type, fieldTypeList())
		}
		if err := checkText(fmt.Sprintf("capability %q: input %q help", c.ID, f.Name), f.Help, maxHelp); err != nil {
			return err
		}
		if err := checkBounds(c.ID, f); err != nil {
			return err
		}
		for _, o := range f.Options {
			// Options are published as an MCP enum and drawn as a select, so
			// they are as much displayed text as Help is.
			if err := checkLine(fmt.Sprintf("capability %q: input %q option %q", c.ID, f.Name, o), o, maxOption); err != nil {
				return err
			}
		}
		// A Default is printed in `--help`, seeded into every form, and
		// published in the MCP tool schema, so it is displayed text wherever
		// it came from. Both shapes are checked: only the string case was,
		// and a StringSlice input's []string default went to models verbatim
		// — the one declared-text hole left in a function whose whole job is
		// that there are none.
		switch d := f.Default.(type) {
		case string:
			if err := checkLine(fmt.Sprintf("capability %q: input %q default", c.ID, f.Name), d, maxHelp); err != nil {
				return err
			}
		case []string:
			for i, e := range d {
				if err := checkLine(fmt.Sprintf("capability %q: input %q default[%d]", c.ID, f.Name, i), e, maxHelp); err != nil {
					return err
				}
			}
		}
		// The same mismatch StatedTypeProblem reports about an operator's
		// config file, one layer earlier: a Default whose Go type is not the
		// one the field declares is read as the zero by every accessor, so
		// `{Type: Bool, Default: "true"}` ships a capability whose declared
		// default is false. Refused at registration rather than reported,
		// because this is the plugin author's own declaration and they are
		// the only person who can fix it.
		if f.Default != nil {
			if problem, hint := StatedTypeProblem(f, f.Default); problem != "" {
				return fmt.Errorf("capability %q: input %q default %s; %s",
					c.ID, f.Name, problem, hint)
			}
		}
		if f.Name == c.Scope {
			scoped = true
		}
	}
	// A Scope naming no input is not a harmless typo: grants would silently
	// stop narrowing to a record and start covering the whole capability.
	if !scoped {
		return fmt.Errorf("capability %q: scope %q names no input", c.ID, c.Scope)
	}
	if err := checkEndpoints(c); err != nil {
		return err
	}
	return nil
}

// checkEndpoints enforces the two rules that are about the set of endpoint
// inputs rather than any one of them.
//
// Both are refusals at registration rather than surprises at dial time,
// because the thing that goes wrong is *where a call goes*, and the operator
// who would have to diagnose it is holding a connection error that names
// nothing. A plugin declaring these wrongly is a bug in the plugin, and it is
// found by loading it.
func checkEndpoints(c Capability) error {
	claimed := map[EndpointRole]string{}
	for _, f := range c.Inputs {
		if f.Endpoint == EndpointNone {
			continue
		}
		// Two inputs claiming one role has no defensible reading: the host
		// would fill whichever it walked into first, so the capability's
		// behaviour would depend on declaration order.
		if first, dup := claimed[f.Endpoint]; dup {
			return fmt.Errorf("capability %q: inputs %q and %q both take the %s endpoint role; "+
				"at most one input may take each", c.ID, first, f.Name, f.Endpoint)
		}
		claimed[f.Endpoint] = f.Name
	}
	// Half an address is not a connection. A plugin declaring only the host
	// would be pointed at 127.0.0.1 on whatever port its own default names —
	// which is a live port on the operator's machine often enough to connect
	// to the wrong thing rather than fail.
	host, hasHost := claimed[EndpointHost]
	port, hasPort := claimed[EndpointPort]
	if hasHost != hasPort {
		have, missing := host, "port"
		if hasPort {
			have, missing = port, "host"
		}
		return fmt.Errorf("capability %q: input %q takes an endpoint role but no input takes %s; "+
			"a tunnel fills an address, and half of one would point the call at %s on this machine "+
			"— declare both, or use the address role for a single input holding host:port",
			c.ID, have, missing, map[bool]string{true: "the plugin's default port", false: "127.0.0.1"}[hasHost])
	}
	return nil
}

// checkBounds rejects a Min/Max the host will never enforce.
//
// The field exists because "remember to clamp" is not a rule a third-party
// author can be expected to follow — `net ping --timeout 0` reached
// time.NewTicker(0) inside a library goroutine and took the process down, and
// over MCP that is one schema-valid call from an unprivileged agent killing
// `rta mcp serve` for every tool attached to it. A bound that is declared and
// silently not applied is worse than no bound at all: the author believes the
// input is clamped and stops checking, and nothing anywhere says otherwise.
//
// Three ways to declare one that does nothing, all of them quiet:
//
//   - A non-numeric value. Resolve reads Min through toInt/toFloat, which
//     return not-ok for a string, so `Min: "1"` means "no minimum" — and the
//     MCP bridge publishes it as the JSON Schema "minimum" keyword regardless,
//     where a string is not a legal value, so the tool schema every connected
//     agent reads is malformed as well.
//   - A bound on a type Resolve does not clamp. Only Int and Float are
//     clamped, so a Min on a string is a promise nothing made.
//   - Min above Max. Clamping applies Min and then Max, so an inverted pair
//     does not error; it pins every value, including valid ones, to Max.
//
// All three were conformance-suite findings, which meant a plugin could fail
// `sdktest` and still register and run. They belong here instead: this is the
// gate every surface goes through, and the suite reports what this returns.
func checkBounds(id string, f Field) error {
	if f.Min == nil && f.Max == nil {
		return nil
	}
	if f.Type != Int && f.Type != Float {
		return fmt.Errorf("capability %q: input %q is %s and declares Min/Max, which apply only to %s and %s",
			id, f.Name, f.Type, Int, Float)
	}
	lo, loOK := toFloat(f.Min)
	hi, hiOK := toFloat(f.Max)
	if f.Min != nil && !loOK {
		return fmt.Errorf("capability %q: input %q has a non-numeric Min (%#v), which is not a bound the host can apply",
			id, f.Name, f.Min)
	}
	if f.Max != nil && !hiOK {
		return fmt.Errorf("capability %q: input %q has a non-numeric Max (%#v), which is not a bound the host can apply",
			id, f.Name, f.Max)
	}
	if loOK && hiOK && lo > hi {
		return fmt.Errorf("capability %q: input %q has Min %v above Max %v, so every value clamps to Max",
			id, f.Name, f.Min, f.Max)
	}
	return nil
}

// reservedInputs are names the host owns on a capability's command line, so a
// capability may not also declare them. Each carries why, because a rule with
// no stated reason is one nobody can check and nobody dares extend.
//
// The failure mode is silence, not a collision. cobra resolves a subcommand's
// own flag before an inherited one, so an input named "dry-run" does not
// conflict with the root's persistent --dry-run: it quietly *becomes* it, and
// the host's copy is never set. `rta acme wipe --yes --dry-run` against such a
// plugin exits 0, reports success, and performs the wipe — the operator asked
// for a preview and the one flag that promised nothing would happen is the
// flag that stopped working.
//
// This map cannot derive the CLI's flag set: it lives in the SDK, which knows
// nothing about cobra or internal/app. So it is the *contract* — the host
// declares what it reserves, and internal/app is tested to stay inside it by
// TestTheCLIReservesEveryNameItOwns. That test is the only thing keeping the
// two in step, which is exactly why this map had one entry while the CLI had
// grown five more names.
//
// Single letters are deliberately absent: an input named "o" registers the
// long flag --o, and pflag keeps long names and shorthands in separate
// namespaces, so -o still reaches --output. Verified, not assumed —
// over-reserving would refuse legitimate names for a collision that does not
// happen.
var reservedInputs = map[string]string{
	"detail": "the host sets it on any surface that owns the whole screen, and Page clears it for embedded sections",
	"dry-run": "the host's promise that a write or destructive capability changed nothing; " +
		"an input of this name makes that promise unkeepable",
	"yes":      "the host's record that a human confirmed a destructive operation",
	"output":   "chooses the renderer, so shadowing it means a caller cannot ask for JSON",
	"no-color": "disables styling, which is what makes rta's output safe to pipe",
	"help":     "cobra's; shadowing it makes `rta <ns> <cap> --help` unreachable",
}

// ReservedInputs lists the names the host owns, sorted.
//
// Exported so the CLI can be tested against it rather than expected to
// remember it: the flag set and this list live in different packages and
// neither derives from the other, so nothing but a test can hold them
// together.
func ReservedInputs() []string {
	out := make([]string, 0, len(reservedInputs))
	for name := range reservedInputs {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// reservedNamespaces are rta's own top-level commands, so a plugin may not
// take one as its namespace.
//
// The same defect as reservedInputs, one level up, and it fails the same way:
// a namespace becomes a top-level command, so a plugin called "doctor"
// *replaces* `rta doctor` — which then prints the plugin's usage and exits 0,
// having run none of the checks an operator ran it for. The check most likely
// to reveal a hostile plugin is the one a hostile plugin can switch off, and
// nothing anywhere says it happened.
//
// RegisterFrom already refuses a namespace another *plugin* holds, which is
// why sys and kv cannot be taken. rta's own commands are not plugins and were
// protected by nothing.
//
// "help" and "completion" are cobra's rather than rta's; they are reserved
// for the same reason and kept in the same list, because the operator does
// not care whose command it is, only that it stopped working.
var reservedNamespaces = map[string]string{
	"completion": "generates the shell completion script",
	"doctor":     "diagnoses the installation, plugins included — the one command that must not be maskable",
	"explain":    "prints what a capability takes and what it is allowed to do",
	"help":       "cobra's help command",
	"init":       "writes a starter configuration",
	"mcp":        "serves and installs the MCP server",
	"plugin":     "lists, installs and scaffolds plugins",
	"policy": "reads and writes the ceiling no grant may exceed — a plugin taking this " +
		"name would shadow the command that reports whether a ceiling is in force, which is " +
		"the one answer that must not come from something an operator has not audited",
	"profile": "rta's own `rta profile` commands, and the RTA_PROFILE_* prefix a " +
		"profile-scoped credential is read from — a plugin namespace of this name could " +
		"produce a LocalEnvVar colliding with one, and would shadow the commands that " +
		"write where a connection goes and which stored entry fills its credential",
	"use": "selects which configured connection this machine's commands run against",
}

// ReservedNamespaces lists the names rta's own commands hold, sorted. Exported
// for the same reason as ReservedInputs: the command tree lives in
// internal/app and this list lives here, so only a test can hold them
// together — TestTheCLIReservesEveryTopLevelCommandItOwns.
func ReservedNamespaces() []string {
	out := make([]string, 0, len(reservedNamespaces))
	for name := range reservedNamespaces {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}
