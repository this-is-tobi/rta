package app

import (
	"math"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/this-is-tobi/rta/internal/mcp"
)

// A tool is chosen by an agent that searches the list: Claude Code defers MCP
// tools and finds them by a query, and Anthropic's tool search tool ranks the
// same four things — a tool's name, its description, its argument names and
// their descriptions. Nothing in the catalogue measured whether a task, said
// in the words a person gives an agent, finds the tool written for it, so a
// description edit could make a tool unfindable and no review would see it.
//
// The ranking here is BM25 over exactly those four, on the surface a client
// is told (the golden test's), not on the declarations: a keyword that never
// reaches the wire cannot help here, and that is the point of asking the wire.
// It is a stand-in for whichever ranker a client runs, with the usual
// parameters, and the table asks for the tool in the first few results rather
// than first: what is held is that a reasonable search lands on it, not that
// one particular ranker puts it on top.

// findable is a task as an agent would phrase it, and the tools that answer it.
var findable = []struct {
	task string
	want []string
}{
	{"how much free space is left on the disk", []string{"sys_disk"}},
	{"which process is using all the memory", []string{"sys_ps"}},
	{"is the machine running hot", []string{"sys_temp"}},
	{"how many cpu cores does this host have", []string{"sys_cpu"}},
	{"what is the load average", []string{"sys_load"}},
	{"how long has the server been up and what kernel does it run", []string{"sys_host"}},
	{"what is taking up space in this folder", []string{"fs_usage"}},
	{"show the layout of a directory", []string{"fs_tree"}},
	{"compute the sha256 of a file", []string{"fs_hash"}},
	{"what time is it in tokyo", []string{"time_at"}},
	{"convert a unix timestamp to a date", []string{"time_at"}},
	{"decode this bearer token", []string{"codec_jwt"}},
	{"base64 decode a string", []string{"codec_b64"}},
	{"url encode a query parameter", []string{"codec_url"}},
	{"what is the thumbprint of this public key", []string{"codec_jwk"}},
	{"what are the strange characters in this text", []string{"debug_ansi"}},
	{"when does the ssl certificate of this site expire", []string{"cert_expiry"}},
	{"which tls version does this server negotiate", []string{"cert_tls"}},
	{"show the certificate chain a server presents", []string{"cert_chain"}},
	{"does this domain have spf and dmarc set up", []string{"audit_mail"}},
	{"scan my dependencies for known vulnerabilities", []string{"audit_deps"}},
	{"which kubernetes components are out of date", []string{"audit_kube_eol"}},
	{"who has cluster-admin in the cluster", []string{"audit_kube_rbac"}},
	{"is python 3.9 still supported", []string{"eol_check"}},
	{"find the commit that changed this line", []string{"git_blame"}},
	{"what did i change since the last commit", []string{"git_diff", "git_status"}},
	{"show the recent commits", []string{"git_log"}},
	{"which branch am i on", []string{"git_branches", "git_overview", "git_status"}},
	{"where does this repository push to", []string{"git_remotes"}},
	{"make a random password", []string{"gen_password"}},
	{"generate a uuid", []string{"gen_uuid"}},
	{"create an api token", []string{"gen_token"}},
	{"is port 5432 open on that host", []string{"net_port", "net_probe"}},
	{"what is listening on this machine", []string{"net_listen"}},
	{"look up the ip address of a hostname", []string{"net_dns"}},
	{"how many hops to reach the server", []string{"net_trace"}},
	{"is the host reachable", []string{"net_ping"}},
	{"which dns servers does this machine use", []string{"net_resolver_list"}},
	{"point a hostname at a local address", []string{"net_hosts_add"}},
	{"what does http status 418 mean", []string{"http_status"}},
	{"call this api endpoint", []string{"http_get", "http_post"}},
	{"remember to do this later", []string{"note_add"}},
	{"find my todo about the migration", []string{"note_search"}},
	{"read a secret from the store", []string{"kv_get"}},
	{"save a password safely", []string{"kv_set"}},
	{"who can decrypt the secret store", []string{"kv_recipients"}},
	{"how many secrets are stored", []string{"kv_overview"}},
	{"how many secrets do i have stored", []string{"kv_overview"}},
	{"how many stored secrets are there", []string{"kv_overview"}},
	{"what is in my secret store", []string{"kv_overview"}},
	{"is the secret store locked", []string{"kv_overview", "kv_status"}},
	{"which folders hold my secrets", []string{"kv_overview", "kv_tree"}},
	{"list my ssh keys", []string{"keys_list"}},
}

// The first few results are what an agent reads: a tool outside them is one it
// does not find.
const findableWithin = 5

func TestATaskInAnAgentsWordsFindsItsTool(t *testing.T) {
	index := newSearchIndex(surface(t, mcp.Options{}))
	var missed []string
	for _, c := range findable {
		top := index.search(c.task, findableWithin)
		if !containsAny(top, c.want) {
			missed = append(missed, c.task+" — wanted "+strings.Join(c.want, " or ")+", got "+strings.Join(top, ", "))
		}
	}
	for _, m := range missed {
		t.Errorf("a task does not find its tool: %s", m)
	}
	if len(missed) > 0 {
		t.Errorf("%d of %d tasks missed: a tool's words are what an agent searches by — say them in its summary, or declare a Keyword for the word a person would use", len(missed), len(findable))
	}
}

func containsAny(got, want []string) bool {
	for _, g := range got {
		for _, w := range want {
			if g == w {
				return true
			}
		}
	}
	return false
}

// searchIndex is a BM25 index over the four things a tool search reads.
type searchIndex struct {
	names []string
	docs  []map[string]int
	size  []int
	freq  map[string]int
	avg   float64
}

func newSearchIndex(tools []tool) *searchIndex {
	idx := &searchIndex{freq: map[string]int{}}
	total := 0
	for _, tl := range tools {
		doc := map[string]int{}
		n := 0
		add := func(text string) {
			for _, w := range searchWords(text) {
				doc[w]++
				n++
			}
		}
		add(tl.Name)
		add(tl.Description)
		props, _ := tl.Schema["properties"].(map[string]any)
		for name, p := range props {
			add(name)
			if prop, ok := p.(map[string]any); ok {
				if text, ok := prop["description"].(string); ok {
					add(text)
				}
			}
		}
		for w := range doc {
			idx.freq[w]++
		}
		idx.names = append(idx.names, tl.Name)
		idx.docs = append(idx.docs, doc)
		idx.size = append(idx.size, n)
		total += n
	}
	idx.avg = float64(total) / float64(len(tools))
	return idx
}

// search is the names of the n best tools for a query.
func (idx *searchIndex) search(query string, n int) []string {
	const k1, b = 1.2, 0.75
	words := map[string]bool{}
	for _, w := range searchWords(query) {
		if !fillerWords[w] {
			words[w] = true
		}
	}
	type scored struct {
		name  string
		score float64
	}
	var out []scored
	for i, doc := range idx.docs {
		score := 0.0
		for w := range words {
			f := float64(doc[w])
			if f == 0 {
				continue
			}
			df := float64(idx.freq[w])
			idf := math.Log(1 + (float64(len(idx.docs))-df+0.5)/(df+0.5))
			score += idf * f * (k1 + 1) / (f + k1*(1-b+b*float64(idx.size[i])/idx.avg))
		}
		if score > 0 {
			out = append(out, scored{idx.names[i], score})
		}
	}
	sort.SliceStable(out, func(a, c int) bool { return out[a].score > out[c].score })
	names := []string{}
	for i := 0; i < len(out) && i < n; i++ {
		names = append(names, out[i].name)
	}
	return names
}

// fillerWords are what a search drops from a query written as a sentence: an
// agent searching a tool list writes the few words that name what it wants,
// and the words that frame a request say nothing about which tool answers it.
var fillerWords = map[string]bool{
	"a": true, "all": true, "and": true, "are": true, "does": true, "has": true, "how": true,
	"i": true, "in": true, "is": true, "it": true, "me": true, "my": true, "of": true, "on": true,
	"or": true, "that": true, "the": true, "this": true, "to": true, "what": true, "which": true,
	"who": true, "do": true, "been": true, "be": true, "from": true, "for": true,
}

func searchWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
