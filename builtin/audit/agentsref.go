package audit

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/this-is-tobi/rta/pkg/findings"
)

// A credential named rather than held.
//
// The fix this audit prints for a credential in a server's env or headers
// block is to move the value into the environment that launches the client.
// Most clients read it back from there through a reference written in the
// same field — "Authorization": "Bearer ${RTA_PAYMENTS_TOKEN}" — and the
// audit failed that reference as a credential in plain text, the answer to
// its own advice. A value that only names a variable holds nothing a reader
// of the file can use, so it is no finding, when the client expands that
// form in that file. Each client spells the reference its own way, and one
// written in another's form is not read: it reaches the server as the text it
// is, a server that then refuses it, and the next edit is the token pasted in
// its place. So such a value stays a failure, and the fix names the form the
// client does read.
//
// Checked against each client's own documentation for the env and headers
// of a server, the two blocks graded here:
//
//   - Claude Code expands ${VAR} and ${VAR:-default} in command, args, env,
//     url and headers, in a project's .mcp.json and in the user and local
//     servers of ~/.claude.json.
//   - Cursor expands ${env:NAME} in command, args, env, url and headers.
//   - VS Code allows its variables in command, args, env, url and headers:
//     ${input:id}, which it asks for once and keeps out of the file, and the
//     predefined ${env:NAME}. Its page on the MCP configuration shows a
//     credential only as an input, in env and in an Authorization header,
//     and never names ${env:NAME}, which it allows only as one of the
//     "predefined variables" a configuration may use, so the fix names the
//     input first.
//   - Gemini CLI resolves $VAR, ${VAR} and ${VAR:-default} in every string of
//     settings.json as it loads it, and %VAR% in a server's env block when it
//     runs on Windows, and on no other system nor in headers (clientOS).
//   - GitHub Copilot CLI reads ${VAR} in a server's env values and takes any
//     other form literally; its headers are read the same way, which its
//     documentation does not state.
//   - Codex CLI takes env and http_headers as static values, and reads a
//     credential from the environment through keys that name the variable:
//     env_vars, env_http_headers and bearer_token_env_var.
//
// A reference's default is text written in the file, so a default holding a
// value is that value in plain text. And a reference beside other text is
// one only when the rest is an authorization scheme and punctuation:
// "sk-live-${SUFFIX}" holds most of a key.

// gradeCredentials grades the credential-named entries of a server's env and
// headers blocks, name being how its rows name the server: a value held in
// the file fails, and so does one naming a variable in a form the client
// does not expand; one naming it in the form the client reads is no finding.
func gradeCredentials(r *agentReport, f agentFile, name string, d serverDecl) {
	refs := refsOf(f)
	for _, block := range []struct {
		entries map[string]string
		header  bool
	}{{d.env, false}, {d.headers, true}} {
		var held, misnamed []string
		named := map[string]string{}
		for k, v := range block.entries {
			if v == "" || !credentialKey.MatchString(k) {
				continue
			}
			switch how, vars := classifyCredential(refs.formsIn(block.header), v); how {
			case credHeld:
				held = append(held, k)
			case credMisnamed:
				misnamed = append(misnamed, k)
				if len(vars) > 0 {
					named[k] = vars[0]
				}
			}
		}
		sort.Strings(held)
		sort.Strings(misnamed)
		gradeHeld(r, f, refs, name, d.name, held, block.header)
		gradeMisnamed(r, f, refs, name, d.name, misnamed, named, block.header)
	}
}

// gradeHeld reports the entries whose value sits in the file.
//
// The names, never the values. This output is read on a terminal, pasted
// into an issue and piped somewhere, and the point of the finding is that the
// value is in a file — putting it on a screen as well would be the tool doing
// the thing it is warning about.
func gradeHeld(r *agentReport, f agentFile, refs clientRefs, name, server string, held []string, header bool) {
	if len(held) == 0 {
		return
	}
	moves := make([]string, len(held))
	for i, k := range held {
		moves[i] = "for " + k + ", " + refs.referWith(k, variableFor(server, k, header), header)
	}
	title := name + " — move " + strings.Join(held, ", ") + " out of " + shortPath(f.path)
	if header {
		r.Add(grpAgentServers, name, findings.Fail,
			"called with "+strings.Join(held, ", ")+" in its headers block, in plain text in "+
				shortPath(f.path)+" — a file every process you run can read", refCredExposed)
		r.addFix("credential", title,
			"A header is the whole credential on this transport, and the file holding it is read "+
				"by every process you run. Take it out of the file — "+strings.Join(moves, "; ")+
				" — or hand it to the client's own credential helper where it has one. Then rotate "+
				"the value that sat in the file: it has been readable since it was written, and "+
				"unlike a launch token it is one a remote server already accepts.")
		return
	}
	r.Add(grpAgentServers, name, findings.Fail,
		"launched with "+strings.Join(held, ", ")+" in its env block, in plain text in "+
			shortPath(f.path)+" — a file every process you run can read", refCredExposed)
	r.addFix("credential", title,
		"The value belongs where the file cannot carry it. Take it out of the file — "+
			strings.Join(moves, "; ")+" — or hand it to the client's own credential helper where it "+
			"has one, or, when the server is rta, to the kv store, referenced from a profile as "+
			"`kv:<name>`, so the config names the secret and never holds it. Then rotate the value "+
			"that sat in the file: it has been readable by every process you ran since it was written.")
}

// gradeMisnamed reports the entries naming a variable in a form the client
// does not expand, which reaches the server as the text it is, named holding
// the variable a braced reference in each names (classifyCredential).
//
// Named by the entry, never by what it holds. A bare $NAME or a %NAME% is how
// a shell names a variable, and also how a password can be spelled —
// "$ECRET_PASSWORD", or a token after "Bearer $" — and read as a variable's
// name it was printed, its first character taken off, in the row and in the
// fix. So the row names the entries, as the one for a value held does, and
// the fix suggests the variable a braced reference named, which no
// credential is spelled as, and otherwise one named for the entry.
func gradeMisnamed(r *agentReport, f agentFile, refs clientRefs, name, server string, misnamed []string,
	named map[string]string, header bool) {
	if len(misnamed) == 0 {
		return
	}
	block, sent, reaches := "env", "launched with ", "the server is launched with that text as written"
	if header {
		block, sent, reaches = "headers", "called with ", "the server is sent that text as written"
	}
	moves := make([]string, len(misnamed))
	for i, k := range misnamed {
		variable := named[k]
		if variable == "" {
			variable = variableFor(server, k, header)
		}
		moves[i] = "for " + k + ", " + refs.referWith(k, variable, header)
	}
	keys := strings.Join(misnamed, ", ")
	r.Add(grpAgentServers, name, findings.Fail,
		sent+keys+" in its "+block+" block as a reference "+refs.client+" does not expand, in "+
			shortPath(f.path)+" — "+reaches, findings.Reference{})
	r.addFix("reference", name+" — spell the reference in "+keys+" as "+refs.client+" reads it",
		keys+" in "+shortPath(f.path)+" names a variable in a form "+refs.client+
			" does not expand, so the server gets the reference itself rather than its value: "+
			strings.Join(moves, "; ")+".")
}

// emptiedTowardRemote are the variables Claude Code reads as empty in a
// remote server's url and headers: set or not, and whatever :-default
// follows one, the server receives nothing where the reference stands. It
// does so, its documentation on MCP says, so that a project's .mcp.json or
// a plugin cannot send Claude Code's own credentials, or a cloud provider's,
// to a server the file names.
//
// **These are the names that documentation gives, and it gives them as
// examples**: "such as" ANTHROPIC_API_KEY and ANTHROPIC_AUTH_TOKEN for
// Claude Code's own, AWS_BEARER_TOKEN_BEDROCK for a cloud provider's,
// HTTPS_PROXY and NPM_TOKEN for others the environment carries. The whole
// set is not published, and it is not the pattern Claude Code strips from a
// headers helper's environment — its page says API_KEY expands as written —
// so a name is recognised here only where the documentation names it, and
// one it does not is left ungraded rather than guessed at.
var emptiedTowardRemote = map[string]bool{
	"ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true, "AWS_BEARER_TOKEN_BEDROCK": true,
	"HTTPS_PROXY": true, "NPM_TOKEN": true,
}

// gradeEmptied warns about a Claude Code remote server whose url or headers
// name a variable Claude Code reads as empty there (emptiedTowardRemote).
//
// Graded as a reference Claude Code expands, such a value was no finding at
// all, and the server was sent an empty credential — "Bearer " and nothing
// after it — and refused every call, usually with a 401, which Claude Code
// reports only as a failed connection. Every header is read and not only a
// credential-named one, since the variable empties wherever it stands. The
// variables are named, which gradeMisnamed does not do: a braced reference to
// one of these names is no spelling of a value.
func gradeEmptied(r *agentReport, f agentFile, name string, d serverDecl) {
	if d.url == "" || refsOf(f).client != "Claude Code" {
		return
	}
	var where []string
	named := map[string]bool{}
	scan := func(entry, value string) {
		hit := false
		for _, m := range refBraces.FindAllStringSubmatch(value, -1) {
			if v := m[refBraces.SubexpIndex("name")]; emptiedTowardRemote[v] {
				named[v], hit = true, true
			}
		}
		if hit {
			where = append(where, entry)
		}
	}
	scan("its url", d.url)
	keys := make([]string, 0, len(d.headers))
	for k := range d.headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		scan(k, d.headers[k])
	}
	if len(where) == 0 {
		return
	}
	vars := make([]string, 0, len(named))
	for v := range named {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	list := strings.Join(vars, ", ")
	r.Add(grpAgentServers, name, findings.Warn,
		"called with "+strings.Join(where, ", ")+" naming "+list+", which Claude Code reads as empty "+
			"toward a remote server — the server receives an empty value there", findings.Reference{})
	r.addFix("emptied", name+" — name "+list+" by a variable of your own",
		"Claude Code reads "+list+" as empty in a remote server's url and headers, set or not and "+
			"whatever default follows, so that a file cannot send its own or a cloud provider's "+
			"credential to a server it names. To give "+name+" a credential, set it in a variable of "+
			"your own in the environment that launches Claude Code and reference that in "+
			shortPath(f.path)+" instead, `"+refsOf(f).spell(variableFor(d.name, "Authorization", true))+
			"` for an Authorization header; and if the value is Claude Code's own key, ask first "+
			"whether "+name+" should hold it at all.")
}

// clientRefs is how one client names a variable of the environment that
// launches it, in the env and headers of a server declaration.
type clientRefs struct {
	client string
	// forms are the references the client expands there, none for a client
	// that expands none, and envForms the ones it expands in an env block
	// and not in headers.
	forms, envForms []*regexp.Regexp
	// spell writes a reference to a variable in the form the client reads,
	// for the fix; nil for a client whose file has no such form.
	spell func(name string) string
}

// Each form captures the variable in "name", and a default in "default"
// where the form has one.
var (
	refBraces    = regexp.MustCompile(`\$\{(?P<name>[A-Za-z_][A-Za-z0-9_]*)(?::-(?P<default>[^}]*))?\}`)
	refBracesBar = regexp.MustCompile(`\$\{(?P<name>[A-Za-z_][A-Za-z0-9_]*)\}`)
	refEnvColon  = regexp.MustCompile(`\$\{env:(?P<name>[A-Za-z_][A-Za-z0-9_]*)\}`)
	refInput     = regexp.MustCompile(`\$\{input:(?P<name>[A-Za-z0-9_.-]+)\}`)
	// A bare $NAME only in the upper case variables are named in: a
	// password that begins with a dollar is plausible, and read as a
	// reference it would lose its failure.
	refBare    = regexp.MustCompile(`\$(?P<name>[A-Z_][A-Z0-9_]*)`)
	refPercent = regexp.MustCompile(`%(?P<name>[A-Z_][A-Z0-9_]*)%`)
)

// anyRef is every form some client expands: a value written only in these is
// a reference, whether or not the client whose file holds it reads it.
var anyRef = []*regexp.Regexp{refBraces, refEnvColon, refInput, refBare, refPercent}

// envBraced are the forms that name a variable of the environment in braces,
// the only ones a report repeats a name from: no credential is spelled like
// one, where a bare $NAME or a %NAME% can be a password (gradeMisnamed), and
// an input's id names no variable at all.
var envBraced = []*regexp.Regexp{refBraces, refEnvColon}

// formsIn are the references the client expands in a headers block, or with
// header false in an env block.
func (c clientRefs) formsIn(header bool) []*regexp.Regexp {
	if header || len(c.envForms) == 0 {
		return c.forms
	}
	return append(slices.Clip(c.forms), c.envForms...)
}

// refsOf is the reference syntax of the client a file belongs to, on the
// system that file belongs to (clientOS).
//
// Gemini CLI's %VAR% is read by that system: its documentation on the env
// block of a server says the form is "supported only when running on
// Windows". Graded the same everywhere, it was failed on Windows as a form
// Gemini does not expand, a finding the operator could only answer by
// rewriting a reference that already works.
func refsOf(f agentFile) clientRefs {
	braces := func(name string) string { return "${" + name + "}" }
	envColon := func(name string) string { return "${env:" + name + "}" }
	for _, c := range []struct {
		prefix string
		refs   clientRefs
	}{
		{"Claude Code", clientRefs{client: "Claude Code", forms: []*regexp.Regexp{refBraces}, spell: braces}},
		{"Cursor", clientRefs{client: "Cursor", forms: []*regexp.Regexp{refEnvColon}, spell: envColon}},
		{"VS Code", clientRefs{client: "VS Code", forms: []*regexp.Regexp{refEnvColon, refInput}, spell: envColon}},
		{"Gemini CLI", clientRefs{client: "Gemini CLI", forms: []*regexp.Regexp{refBraces, refBare}, spell: braces}},
		{"GitHub Copilot CLI", clientRefs{client: "GitHub Copilot CLI", forms: []*regexp.Regexp{refBracesBar}, spell: braces}},
		{"Codex CLI", clientRefs{client: "Codex CLI"}},
	} {
		if strings.HasPrefix(f.label, c.prefix) {
			if c.prefix == "Gemini CLI" && clientOS == "windows" {
				c.refs.envForms = []*regexp.Regexp{refPercent}
			}
			return c.refs
		}
	}
	return clientRefs{client: "the client"}
}

// authSchemes are the words a header puts before its credential.
var authSchemes = map[string]bool{"bearer": true, "basic": true, "token": true}

// onlyReferences reports whether value names its credential only through
// references in forms, and which variables: every reference taken out, what
// is left is at most an authorization scheme and punctuation. A default
// counts as left, since it is written in the file.
func onlyReferences(value string, forms []*regexp.Regexp) ([]string, bool) {
	var names []string
	rest := value
	for _, form := range forms {
		name, def := form.SubexpIndex("name"), form.SubexpIndex("default")
		rest = form.ReplaceAllStringFunc(rest, func(ref string) string {
			m := form.FindStringSubmatch(ref)
			names = append(names, m[name])
			if def > 0 {
				return " " + m[def] + " "
			}
			return " "
		})
	}
	if len(names) == 0 {
		return nil, false
	}
	rest = strings.TrimSpace(rest)
	if scheme, after, _ := strings.Cut(rest, " "); authSchemes[strings.ToLower(scheme)] {
		rest = after
	}
	return names, !strings.ContainsFunc(rest, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) })
}

// credentialValue is how one credential-named entry of an env or headers
// block holds its value: named through a reference the client reads, named
// in a form it does not, or held.
type credentialValue int

const (
	credHeld credentialValue = iota
	credReferenced
	credMisnamed
)

// classifyCredential is how value holds its credential, given the forms its
// client expands where it stands, and for one named in a form the client does
// not expand, the variables it names in braces (envBraced), which a report may
// repeat.
func classifyCredential(forms []*regexp.Regexp, value string) (credentialValue, []string) {
	if _, ok := onlyReferences(value, forms); ok {
		return credReferenced, nil
	}
	if _, ok := onlyReferences(value, anyRef); ok {
		braced, _ := onlyReferences(value, envBraced)
		return credMisnamed, braced
	}
	return credHeld, nil
}

// variableFor is a variable name for the fix to suggest: the entry's own
// name for an env block, and for a header one made of the server's, since a
// header's name says nothing about which credential it carries.
func variableFor(server, key string, header bool) string {
	upper := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return unicode.ToUpper(r)
			}
			return '_'
		}, s)
	}
	if !header {
		return key
	}
	if strings.EqualFold(key, "Authorization") {
		return upper(server) + "_TOKEN"
	}
	return upper(server) + "_" + upper(key)
}

// referWith is the sentence a fix gives for naming variable from the file
// rather than holding its value there, in the form this client reads, for an
// entry key of an env block or, with header, of a headers block.
func (c clientRefs) referWith(key, variable string, header bool) string {
	switch {
	case c.client == "VS Code":
		id := strings.ToLower(strings.ReplaceAll(variable, "_", "-"))
		return "declare an input in the file's inputs, `{ \"type\": \"promptString\", \"id\": \"" + id +
			"\", \"password\": true }`, and reference it as `${input:" + id + "}`, which VS Code asks for " +
			"when the server first starts and keeps in its own secret storage rather than the file, or " +
			"as `" + c.spell(variable) + "` with " + variable + " set in the environment that launches VS Code"
	case c.spell != nil:
		return "reference it from the file as `" + c.spell(variable) + "`, which " + c.client +
			" expands from the environment that launches it, and set " + variable + " there"
	case c.client == "Codex CLI" && header:
		return "drop it from http_headers, which Codex CLI sends as written, and name the variable " +
			"instead: `env_http_headers = { \"" + key + "\" = \"" + variable + "\" }` reads the header from " +
			variable + " in the environment that launches Codex CLI, and `bearer_token_env_var = \"" +
			variable + "\"` does the same for a bearer token in Authorization"
	case c.client == "Codex CLI":
		return "drop it from env, which Codex CLI passes as written, and list it in `env_vars = [\"" +
			variable + "\"]`, which forwards " + variable + " from the environment that launches Codex CLI"
	}
	return "set it in the environment that launches the client"
}
