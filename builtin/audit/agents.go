package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Auditing the thing that is asking.
//
// An AI agent's configuration decides more about a machine's exposure than
// most of what this plugin grades, and nothing reads it. It says which tools
// the model may run, which MCP servers launch on start, what credentials
// those servers are handed, and which endpoint every prompt is sent to — and
// all of it lives in a JSON file somebody edited once and has not opened
// since.
//
// **It reads and never writes**, which is the same line `rta mcp install`
// draws and for the stronger of its reasons: that file is what grants an
// agent access to your secrets, and a tool whose whole argument is that
// consent should be visible and deliberate has no business editing five
// agents' permission files. Every finding here names the file and the line
// of thinking; the operator makes the change.
//
// **And it refuses over MCP**, which is not tidiness. The subject of this
// audit is the agent asking for it: the findings are "your shell is
// unrestricted", "this server holds a token", "here is where each one keeps
// its credential" — a map of the machine, read out to the thing it is a map
// of. An audit an agent runs on its own leash is not an audit.

// Parsing is deliberately shallow and structural rather than per-client.
//
// Five clients share three schemas today and will not tomorrow: Claude Code
// keeps servers under `mcpServers` and again under `projects.<dir>.mcpServers`,
// VS Code calls the same thing `servers`, Codex writes TOML. Modelling each
// one is a table that is wrong the week after a release. So this walks the
// JSON looking for the *shape* a server declaration has — an object with a
// `command`, optionally an `env` — wherever it sits and however deep. A shape
// that moves keeps being found; a schema that changes does not.

// agentFile is one configuration worth reading, and how to say where it came
// from when a finding names it.
type agentFile struct {
	label string // "Claude Code", "VS Code"
	path  string
}

// agentFiles are the files each client actually keeps, resolved for this
// machine. Deliberately separate from mcpinstall.go's table, which holds a
// sentence for a human to read ("~/.cursor/mcp.json (or .cursor/mcp.json for
// one project)") rather than a path to open — one of them has to be prose and
// one has to be openable, and conflating them would make the install output
// worse to keep this shorter.
func agentFiles(home, wd string) []agentFile {
	var out []agentFile
	add := func(label string, parts ...string) {
		out = append(out, agentFile{label: label, path: filepath.Join(parts...)})
	}
	add("Claude Code", home, ".claude", "settings.json")
	add("Claude Code", home, ".claude", "settings.local.json")
	add("Claude Code", home, ".claude.json")
	add("Claude Code (this project)", wd, ".mcp.json")
	add("Claude Code (this project)", wd, ".claude", "settings.json")
	add("Claude Code (this project)", wd, ".claude", "settings.local.json")
	add("Cursor", home, ".cursor", "mcp.json")
	add("Cursor (this project)", wd, ".cursor", "mcp.json")
	add("Gemini CLI", home, ".gemini", "settings.json")
	add("GitHub Copilot CLI", home, ".copilot", "mcp-config.json")
	add("Codex CLI", home, ".codex", "config.toml")
	switch runtime.GOOS {
	case "darwin":
		add("VS Code", home, "Library", "Application Support", "Code", "User", "mcp.json")
	case "windows":
		add("VS Code", home, "AppData", "Roaming", "Code", "User", "mcp.json")
	default:
		add("VS Code", home, ".config", "Code", "User", "mcp.json")
	}
	return out
}

// Groups order the detail page.
var (
	grpAgentTools   = group{"tools", "what the agent may run"}
	grpAgentServers = group{"servers", "mcp servers"}
	grpAgentModel   = group{"model", "where prompts go"}
	grpAgentFiles   = group{"files", "the files themselves"}
)

var agentsGroupOrder = []group{grpAgentTools, grpAgentServers, grpAgentModel, grpAgentFiles}

// agentReport carries the ordinary findings plus the edits that answer them.
//
// A fix is collected at the same call site as the finding it answers, never
// re-derived from the finding's prose afterwards: prose is for reading, and a
// remediation reconstructed by matching sentences is one reword away from
// fixing the wrong thing. Collected always, rendered only under --fix — the
// cost of composing a string nobody asked for is nothing, and the alternative
// is two walks over the same files that can disagree.
type agentReport struct {
	report
	fixes []agentFix
}

// agentFix is one edit the operator can make, spelled exactly.
type agentFix struct {
	id    string // stable section id, per fix kind
	title string // what it fixes, naming the client
	body  string // the edit itself, ready to act on
}

func (r *agentReport) addFix(id, title, body string) {
	r.fixes = append(r.fixes, agentFix{id: id, title: title, body: body})
}

// fixPage renders the remediation view: one section per finding that has a
// mechanical edit, the edit spelled out. **It prints and never writes** —
// the same line the package comment draws, restated as a feature: the file
// that grants an agent access to your secrets changes by your hand, with the
// change already written for you. Findings with no mechanical answer (a
// redirected endpoint that may be your gateway, a credential in this shell's
// environment) print nothing here; the grades are where they live.
func fixPage(r *agentReport) view.View {
	if len(r.fixes) == 0 {
		return view.Text{Body: "nothing to paste — no finding on this machine has a mechanical fix. " +
			"`rta audit agents` has the grades themselves."}
	}
	s := view.Sections{}
	for i, f := range r.fixes {
		// The index keeps ids unique when one kind of fix applies to two
		// files — two group-readable configs are two sections, not one
		// overwriting the other.
		s.Items = append(s.Items, view.Section{
			ID: fmt.Sprintf("%s-%d", f.id, i), Title: f.title, View: view.Text{Body: f.body}})
	}
	return s
}

func runAgents(ctx context.Context, req plugin.Request) (view.View, error) {
	if req.Surface() == plugin.SurfaceMCP {
		return nil, view.Errorf("audit.agents.mcp",
			"this audit is about the agent asking for it, so it does not answer over MCP").
			WithHint("run `rta audit agents` yourself — the findings are a map of what the " +
				"agent may reach, and reading it out to the agent is the thing they are for")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, view.Errorf("audit.agents.home", "finding your home directory: %v", err)
	}
	wd, _ := os.Getwd()

	r := &agentReport{}
	var found int
	var claudeSeen bool
	for _, f := range agentFiles(home, wd) {
		info, err := os.Stat(f.path)
		if err != nil || info.IsDir() {
			continue
		}
		found++
		if strings.HasPrefix(f.label, "Claude Code") {
			claudeSeen = true
		}
		auditFileMode(r, f, info.Mode().Perm())
		if strings.EqualFold(filepath.Ext(f.path), ".json") {
			auditAgentJSON(r, f)
		}
	}
	if found == 0 {
		r.add(grpAgentFiles, "config", stInfo,
			"no agent configuration found in the usual places — nothing to grade", reference{})
	}
	auditModelEndpoint(&r.report)
	auditRtaReach(r, claudeSeen)

	if req.Bool("fix") {
		return fixPage(r), nil
	}
	if req.Bool("detail") {
		summary := append([]view.Pair{
			{Key: "files read", Value: plural(found, "file")},
		}, r.grade()...)
		return detailPage(ctx, req, &r.report, agentsGroupOrder, view.KeyValue{Pairs: summary}), nil
	}
	return r.table(true), nil
}

// auditFileMode grades who else on this machine can read the file.
//
// It is graded for every agent config and not only the ones holding a
// credential, because the list of servers and the paths they are pointed at
// is worth protecting on its own — and because the credential arrives later,
// in an edit nobody re-audits.
func auditFileMode(r *agentReport, f agentFile, mode os.FileMode) {
	if runtime.GOOS == "windows" {
		return // POSIX bits mean nothing here; the ACL is the real answer.
	}
	if mode&0o077 != 0 {
		r.addFix("chmod", f.label+" — make "+shortPath(f.path)+" yours alone",
			"chmod 600 "+shortPath(f.path)+"\n\n"+
				"The finding is about who else on this machine can read the file; 600 is nobody.")
	}
	switch {
	case mode&0o007 != 0:
		r.add(grpAgentFiles, shortPath(f.path), stFail,
			f.label+" config is world-readable ("+mode.String()+") — every account on this "+
				"machine can read what it holds", refCredExposed)
	case mode&0o070 != 0:
		r.add(grpAgentFiles, shortPath(f.path), stWarn,
			f.label+" config is group-readable ("+mode.String()+")", refCredExposed)
	default:
		r.add(grpAgentFiles, shortPath(f.path), stOK, f.label+" config is yours alone ("+mode.String()+")",
			refCredExposed)
	}
}

// auditAgentJSON walks one file for the shapes worth grading.
func auditAgentJSON(r *agentReport, f agentFile) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		r.add(grpAgentFiles, shortPath(f.path), stWarn,
			f.label+" config could not be read: "+clip(err.Error()), reference{})
		return
	}
	var doc any
	if json.Unmarshal(data, &doc) != nil {
		// Not a failure worth grading: several of these files are JSONC, and
		// a comment is not a security finding.
		r.add(grpAgentFiles, shortPath(f.path), stInfo,
			f.label+" config is not plain JSON (comments are allowed in some of these), "+
				"so only its permissions were graded", reference{})
		return
	}
	servers := map[string]serverDecl{}
	collectServers(doc, servers)
	gradeServers(r, f, servers)
	gradePermissions(r, f, doc)
}

// serverDecl is what one MCP server declaration says, whatever key it was
// found under.
type serverDecl struct {
	command string
	args    []string
	env     map[string]string
}

// collectServers walks any JSON for objects shaped like a server declaration.
//
// Keyed by name so the same server declared in a user file and a project file
// is graded once — which is the ordinary case for anyone who has both.
func collectServers(node any, out map[string]serverDecl) {
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			obj, ok := child.(map[string]any)
			if !ok {
				collectServers(child, out)
				continue
			}
			if cmd, isServer := obj["command"].(string); isServer {
				d := serverDecl{command: cmd, env: map[string]string{}}
				if raw, ok := obj["args"].([]any); ok {
					for _, a := range raw {
						if s, ok := a.(string); ok {
							d.args = append(d.args, s)
						}
					}
				}
				if raw, ok := obj["env"].(map[string]any); ok {
					for k, val := range raw {
						if s, ok := val.(string); ok {
							d.env[k] = s
						}
					}
				}
				out[key] = d
				continue
			}
			collectServers(child, out)
		}
	case []any:
		for _, child := range v {
			collectServers(child, out)
		}
	}
}

// credentialKey names an environment variable that holds one. The same
// pattern builtin/git masks config values by, widened by the two spellings
// an MCP server declaration actually uses.
var credentialKey = regexp.MustCompile(`(?i)(token|password|passwd|secret|api[_-]?key|credential|auth)`)

// gradeServers reports what each declared server is handed and where it comes
// from.
func gradeServers(r *agentReport, f agentFile, servers map[string]serverDecl) {
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		d := servers[name]
		var holds []string
		for k, v := range d.env {
			if v != "" && credentialKey.MatchString(k) {
				holds = append(holds, k)
			}
		}
		sort.Strings(holds)
		if len(holds) > 0 {
			// The names, never the values. This output is read on a terminal,
			// pasted into an issue and piped somewhere, and the point of the
			// finding is that the value is in a file — putting it on a screen
			// as well would be the tool doing the thing it is warning about.
			r.add(grpAgentServers, name, stFail,
				"launched with "+strings.Join(holds, ", ")+" in its env block, in plain text in "+
					shortPath(f.path)+" — a file every process you run can read", refCredExposed)
			// Prose and no snippet, deliberately: there is no syntax for this
			// fix that is true across clients, and a pasted edit that only
			// works in one of them is worse than naming the move.
			r.addFix("credential", name+" — move "+strings.Join(holds, ", ")+" out of "+shortPath(f.path),
				"The value belongs where the file cannot carry it: the environment that launches "+
					"the client, the client's own credential helper where it has one, or — when the "+
					"server is rta — the kv store, referenced from a profile as `kv:<name>`, so the "+
					"config names the secret and never holds it. Then rotate the value that sat in "+
					"the file: it has been readable by every process you ran since it was written.")
		}
		if fetch := fetchOnLaunch(d); fetch != "" {
			r.add(grpAgentServers, name, stWarn,
				"launched with `"+fetch+"`, which fetches and runs whatever the registry serves "+
					"at that moment — no version pinned, no digest checked", refUnpinnedDep)
			r.addFix("pin", name+" — pin what launches with your editor",
				pinFix(fetch, f))
		}
	}
}

// fetchOnLaunch reports the package-fetching runner a server is started
// through, if any.
//
// It is a supply-chain finding and not a style one: the config says "run the
// latest of this package", so the code that launches with your editor is
// whatever was published most recently, executed before anybody reads a
// changelog. Pinning a version is one edit and turns it into a decision.
func fetchOnLaunch(d serverDecl) string {
	base := filepath.Base(d.command)
	runners := map[string]bool{"npx": true, "uvx": true, "pipx": true, "bunx": true, "pnpm": true}
	if !runners[base] {
		return ""
	}
	// A pinned spec — pkg@1.2.3 — is a decision somebody made, so it is not
	// this finding. A bare name, a tag, or `@latest` is.
	for _, a := range d.args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		at := strings.LastIndex(a, "@")
		if at > 0 && a[at+1:] != "latest" && strings.ContainsAny(a[at+1:], "0123456789") {
			return ""
		}
		return base + " " + a
	}
	return base
}

// gradePermissions reads Claude Code's own tool allowlist, which is the
// setting that decides whether anything else here is enforceable.
//
// Claude-specific on purpose, and named as such: it is the only one of these
// clients that writes its tool permissions to a file rta can read. The others
// get no row rather than a guess.
func gradePermissions(r *agentReport, f agentFile, doc any) {
	root, ok := doc.(map[string]any)
	if !ok {
		return
	}
	perms, ok := root["permissions"].(map[string]any)
	if !ok {
		return
	}
	if mode, ok := perms["defaultMode"].(string); ok && strings.EqualFold(mode, "bypassPermissions") {
		r.add(grpAgentTools, "permission mode", stFail,
			"defaultMode is bypassPermissions in "+shortPath(f.path)+
				" — every tool runs without asking, which is every gate below it turned off",
			refExcessivePriv)
		r.addFix("bypass", f.label+" — turn bypassPermissions off",
			"Delete the defaultMode line from "+shortPath(f.path)+", or set a mode that asks:\n\n"+
				"  \"permissions\": {\n"+
				"    \"defaultMode\": \"acceptEdits\"\n"+
				"  }\n\n"+
				"Every other grade this audit hands out sits below that switch; while it is on, "+
				"each of them is theoretical.")
		return
	}
	allow, _ := perms["allow"].([]any)
	for _, entry := range allow {
		rule, ok := entry.(string)
		if !ok {
			continue
		}
		// `Bash` with no bracketed pattern is every command. `Bash(git
		// status:*)` is a decision; `Bash` is the absence of one.
		if strings.EqualFold(strings.TrimSpace(rule), "bash") {
			r.add(grpAgentTools, "shell", stFail,
				"`Bash` is allowed unrestricted in "+shortPath(f.path)+
					" — the agent can run any command, which includes every tool that reaches "+
					"the things rta gates", refExcessivePriv)
			r.addFix("shell", f.label+" — scope the shell",
				"Replace the bare \"Bash\" entry in "+shortPath(f.path)+" with the command "+
					"families you mean to hand over — each entry is a decision:\n\n"+
					"  \"permissions\": {\n"+
					"    \"allow\": [\n"+
					"      \"Bash(git status:*)\",\n"+
					"      \"Bash(git diff:*)\",\n"+
					"      \"Bash(go test:*)\"\n"+
					"    ]\n"+
					"  }\n\n"+
					"The entries are examples; choosing what belongs in the list is the fix. This "+
					"is the setting that moves an agent from a shell rta cannot bound to a tool "+
					"list it can.")
			return
		}
	}
}

// pinFix spells the edit for a server fetched at launch. The registry lookup
// differs per runner, so the sentence naming it is chosen rather than generic
// where the runner is known.
func pinFix(fetch string, f agentFile) string {
	pkg := ""
	if parts := strings.Fields(fetch); len(parts) > 1 {
		pkg = parts[1]
	}
	body := "Pin the package in " + shortPath(f.path) + ", so what launches with your editor " +
		"is what you read and not what was published overnight:\n\n" +
		"  \"args\": [\"" + orElse(pkg, "<package>") + "@1.2.3\"]\n\n" +
		"Replace 1.2.3 with the current release and bump it deliberately"
	switch strings.Fields(fetch)[0] {
	case "npx", "bunx", "pnpm":
		body += " — `npm view " + orElse(pkg, "<package>") + " version` prints it."
	case "uvx", "pipx":
		body += " — `pip index versions " + orElse(pkg, "<package>") + "` lists them."
	default:
		body += "."
	}
	return body
}

// orElse fills a hole a shallow parse could not, so a fix never prints an
// empty spot where a name belongs.
func orElse(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// auditModelEndpoint reports a redirected model endpoint.
//
// It is the highest-consequence setting on this list and the one nothing
// warns about: every prompt an agent sends — including whatever it just read
// out of your repository — goes to whatever host this names, and whatever
// that host returns is what the agent then acts on. A proxy you run is a
// normal setup; a proxy you do not remember setting is an exfiltration
// channel and a prompt-injection channel at once.
//
// Reported and never judged: rta cannot know whose gateway that is.
func auditModelEndpoint(r *report) {
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_URL", "OPENAI_BASE_URL", "OPENAI_API_BASE"} {
		v := strings.TrimSpace(os.Getenv(key))
		if v == "" {
			continue
		}
		r.add(grpAgentModel, "endpoint", stWarn,
			key+" points this shell's agents at "+v+
				" — every prompt goes there, and what comes back is what the agent acts on. "+
				"Deliberate if it is your gateway; worth knowing either way", refInfoExposure)
	}
	for _, key := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		if os.Getenv(key) != "" {
			r.add(grpAgentModel, "model credential", stInfo,
				key+" is set in this shell, so anything you launch from it inherits the key",
				refCredExposed)
		}
	}
}

// auditRtaReach states the boundary this whole tool rests on, out loud.
//
// It is an `info` row and it is the most important one on the page. rta gates
// what an agent may do *over MCP*; the same agent with a shell can run the
// rta binary directly, issue itself a grant, and use it — and the CLI writes
// nothing to the agent ledger, so none of that appears in the record. rta
// cannot fix this and should not pretend to: it is software running as you,
// and so is the agent.
//
// What it can do is stop the misunderstanding, which is the one that makes
// people trust a boundary further than it goes.
func auditRtaReach(r *agentReport, claudeSeen bool) {
	// The path is last and the point is first, because the compact table
	// clips: "an agent that can run commands can run /very/long/path…" would
	// spend the whole row on a path and lose the sentence it is there for.
	detail := "an agent that can run commands can run rta itself: issue a grant, then call " +
		"capabilities the MCP record never sees. rta bounds an agent with no shell; with one, " +
		"these gates are hygiene rather than containment"
	if self, err := os.Executable(); err == nil {
		detail += " (" + shortPath(self) + ")"
	}
	r.add(grpAgentTools, "rta itself", stInfo, detail, refExcessivePriv)
	// Only where there is a file for the paste to land in: Claude Code is the
	// one client whose permission rules live somewhere this audit reads, and
	// handing another client's user an edit for a file they do not have is a
	// fix that fixes nothing.
	if claudeSeen {
		r.addFix("rta-deny", "Claude Code — deny rta's own authority-expanding commands",
			"The ordinary shape of an agent reaching past rta's gates is a handful of its own "+
				"commands: issuing itself a grant, answering its own parked request, reading the "+
				"store, trusting a plugin. Deny them by name, in ~/.claude/settings.json:\n\n"+
				"  \"permissions\": {\n"+
				"    \"deny\": [\n"+
				"      \"Bash(rta grant:*)\",\n"+
				"      \"Bash(rta agent:*)\",\n"+
				"      \"Bash(rta kv:*)\",\n"+
				"      \"Bash(rta plugin trust:*)\",\n"+
				"      \"Bash(rta plugin allow:*)\",\n"+
				"      \"Bash(rta plugin install:*)\"\n"+
				"    ]\n"+
				"  }\n\n"+
				"A string match is a seatbelt and not a wall — a shell can reach the same commands "+
				"other ways, and rta's own gates stay where they are precisely because of that. "+
				"What it refuses is the ordinary reach, and the ordinary reach is what happens. "+
				"The agent keeps every capability you granted it over MCP, where calls are "+
				"consented, bounded and recorded.")
	}
}

// shortPath puts ~ back, so a finding names a path the reader recognises and
// does not print their username into an issue.
func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
