package app

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// A page that pipes `-o json` into jq tells a reader which key the answer has,
// and a reader copies the line. The quickstart told one to run
// `rta sys cpu -o json | jq '.rows'` on an answer whose key is `pairs`: the
// command printed null, exit status 0, and the page looked fine until a person
// tried it. The spelling and parsing tests beside this one stop at the pipe, so
// the half of the line that says what the answer holds was checked by nobody.
//
// So the first key each jq filter reads is looked up in the answer of the
// command that feeds it. Only the commands that answer the same on every
// machine are run — the ones that look at this host, make a value, or read
// rta's own state — and a page's other pipelines are left to whoever wrote
// them, since running a network call from a test to check a key name would
// make a flaky test of a documentation typo.

// answersOffline are the first words of the commands whose answer needs
// nothing from the network, a repository or a cluster, and so can be run.
var answersOffline = map[string]bool{
	"sys": true, "gen": true, "time": true, "codec": true, "debug": true,
	"profile": true, "agent": true, "plugin": true, "grant": true,
}

// jqKey is the first key a jq filter reads from the top of its input.
var jqKey = regexp.MustCompile(`\.([A-Za-z_][A-Za-z0-9_]*)`)

// pipedJSON is a documented line that pipes an rta answer, in json, into jq,
// as the words of the rta command and the jq filter that reads it.
func pipedJSON(line string) (words []string, filter string, ok bool) {
	head, tail, found := strings.Cut(line, "| jq")
	if !found {
		return nil, "", false
	}
	words, ok = plainCommand(head)
	asksJSON := slices.Contains(words, "json") && (slices.Contains(words, "-o") || slices.Contains(words, "--output"))
	if !ok || !asksJSON {
		return nil, "", false
	}
	return words, tail, true
}

func TestEveryKeyTheDocsPipeIntoJqIsOneTheAnswerHas(t *testing.T) {
	repo := repoRoot(t)
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	pages, err := filepath.Glob(filepath.Join(repo, "docs", "*", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	top, _ := filepath.Glob(filepath.Join(repo, "docs", "*.md"))
	pages = append(append(pages, top...), filepath.Join(repo, "README.md"))

	checked := 0
	for _, page := range pages {
		rel, _ := filepath.Rel(repo, page)
		for _, line := range shellLines(readDoc(t, repo, rel)) {
			words, filter, ok := pipedJSON(line.text)
			if !ok || !answersOffline[words[0]] {
				continue
			}
			m := jqKey.FindStringSubmatch(filter)
			if m == nil {
				continue
			}
			config := filepath.Join(t.TempDir(), "config.yaml")
			out, errOut, err := runConfigAt(t, reg, config, words...)
			if err != nil {
				t.Errorf("%s:%d: `%s` failed, so the key it pipes cannot be checked: %v\n%s", rel, line.line, strings.TrimSpace(line.text), err, errOut)
				continue
			}
			var answer map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &answer); err != nil {
				t.Errorf("%s:%d: `%s` did not answer a JSON object: %v", rel, line.line, strings.TrimSpace(line.text), err)
				continue
			}
			checked++
			if _, has := answer[m[1]]; !has {
				keys := make([]string, 0, len(answer))
				for k := range answer {
					keys = append(keys, k)
				}
				slices.Sort(keys)
				t.Errorf("%s:%d: `%s` reads .%s, and the answer has %s", rel, line.line,
					strings.TrimSpace(line.text), m[1], strings.Join(keys, ", "))
			}
		}
	}
	if checked < 3 {
		t.Fatalf("only %d pipelines were checked; the test no longer finds the lines it was written for", checked)
	}
}
