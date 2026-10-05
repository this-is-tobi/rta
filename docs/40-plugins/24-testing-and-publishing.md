# Testing, conventions and publishing

## Testing

`rta plugin new` ships a `main_test.go` wired to `pkg/sdk/sdktest`, so `go test` passes from the first minute:

```go
func TestPlugin(t *testing.T) {
	sdktest.Check(t, Plugin(), sdktest.WithInputs(conformanceInputs), sdktest.WithSource("."))
}
```

It runs the catalogue-wide invariants rta holds its own built-ins to — the shared verb vocabulary, every declared view rendering in every format it claims, dry-run honesty on anything that writes, declared text, and the hints your source words at run time, that spell no flag for an agent to look for ([Declared text is checked](./23-safety-and-credentials.md#declared-text-is-checked)). It found a built-in sending real bytes on `--dry-run` the first time it was pointed at rta's own catalogue.

What it runs is worth knowing before you point it at a connection. Each `Read` capability is run once, for real, with its declared defaults — whatever it reads, it reads — except one that declares `NoPreview`, which it skips and says so. A `Write` or `Destructive` capability is never run for real: it is run once with `DryRun` set, against a temporary `RTA_DATA_DIR`, and the suite fails if the directory changed. It cannot see what leaves the machine, so a dry run that sends a request is still yours to get right; that is what pointing a connection input at `127.0.0.1:1` below is for. The test sets `RTA_DATA_DIR` for its duration, so it must not be parallel.

`sdktest.Skip(rule, capabilityID, why)` opts one capability out of one rule, and the reason is printed on every run so a rule never quietly stops applying: `sdktest.RuleDryRun` (the dry run leaves the data directory as it was), `RuleViews` (every view survives the JSON encoding), `RuleVerbs` (the last ID segment is a verb the catalogue uses, a warning only), `RuleRedaction` (a secret in an answer is marked), `RuleActions` (what the TUI may do with a result holds against the result), `RuleSpelling` (the declared text spells nothing only a terminal can act on, by capability ID, or by the plugin's name for its own summary), and `RuleWording` (what an agent is shown stays in budget and speaks to an agent, and a time says its unit). The declaration check, the one registration makes, has no waiver, and neither has the source check `WithSource` adds.

**Fill in `conformanceInputs` as you add capabilities.** The suite cannot invent a bucket name or a record id, so a capability with a required input and no value here is one it cannot drive — and almost every capability that *changes* something has one. A `Write` or `Destructive` capability the suite could not drive is a failure, not a skip, and the message names both ways out: supply a value, or state why not with `sdktest.Skip`. This is not defensiveness. rta's own external plugins each called `Check`, each went green, and behind that six handlers wrote to real systems under `--dry-run` because not one of them was ever run.

Two rules make the difference between a real check and a green nothing:

```go
func conformanceInputs(dir string) map[string]map[string]any {
	return map[string]map[string]any{
		// Point anything that connects at somewhere nothing is listening, so
		// a dry run that stops being dry fails as a refused connection rather
		// than as a request against somebody's real service.
		"acme.thing.set": {"endpoint": "127.0.0.1:1", "name": "conformance"},
		// Point every path inside dir, which is the directory the suite
		// watches — a write that should not have happened is then a test
		// failure rather than a file in a temp directory nobody looks at.
		"acme.thing.get": {"endpoint": "127.0.0.1:1", "out": filepath.Join(dir, "got")},
	}
}
```

Reads that cannot be driven stay a log line: that is missing coverage, not a broken promise, and demanding a live target for every diagnostic is exactly what rta's own fixture deliberately refuses to do.

The values you supply go through the same input check the host puts in front of your handler, so one outside a field's `Options` or its `Min` and `Max` is not run: the host would refuse it before your handler saw it, and a result for a call that cannot happen checks nothing. The suite says so, and for a `Write` or `Destructive` capability that is a failure, like a missing value.

## Conventions worth following

Use the shared verb vocabulary. `sdktest` warns on a novel verb, and when your word has a standard spelling it names it — it will tell you rta writes `delete` as `rm`, in four places already. The whole list:

```
add  done  edit  get  init  inspect  list  overview
reopen  rm  search  set  show  status  tags  toggle
```

`sdktest.Vocabulary()` returns the same list at runtime, so you never have to trust this page over the tool. The point is that learning one plugin teaches you all of them.

One of those words carries extra weight. **`<your-plugin>.overview` becomes your dashboard tile** — the panel the TUI draws for your plugin on its landing screen. Without one, the tile is whichever of your capabilities happens to come first and can run unattended, which means your declaration order picks it by accident. Declaring an overview is how you pick it on purpose.

A tile runs on load and then every few seconds with nobody watching, so it has to be `Read`, answerable from its defaults alone, and cheap enough to repeat. If your overview is none of those, set `NoPreview: true` on it — rta will tile something else rather than put it on a timer, and `overview` still means to a reader what it means everywhere else.

A capability that is worth a tile but not at that pace — an answer that moves by the day and costs a network round trip to compute — declares `Refresh: 2 * time.Hour` and the dashboard waits that long between runs. It is how a person who adds your `NoPreview` capability to their dashboard (`rta dashboard add`) gets it at a pace you chose rather than one the host guessed; the automatic dashboard still leaves it out.

A tile is small, and the same capability can have a bigger page: declare `Detailed: true` and the host sets the boolean `detail` input when it has the whole screen — a tile opened, a selection in browse — and leaves it false for the compact view, which is what the dashboard draws. At a terminal it is `--detail`. `detail` is the host's own name, so you never declare an input for it; your handler branches on `req.Bool("detail")`. A detail page is assembled from views your plugin already returns, with `plugin.NewPage(ctx, req)`: `Add`/`AddAs` run a sibling handler and append its view as a section, `Put`/`PutAs` append a view you hold, `Run` runs one without appending so a failure is yours to decide, and `p.View()` is what you return. Every section runs with the page's own inputs, so one that connects reaches what the caller reached, and a section that fails is dropped and recorded as a warning rather than costing the reader the rest — say so with `Put` and an explanation when the absence is itself the finding. Only a `Read` handler can be embedded, and the call states it — `p.AddAs("stations", "nearby stations", listStations, plugin.Read, nil)`, the last argument being values that overlay the page's inputs — because a page composes calls directly and nothing in it could gate a `Write`: any other class panics the first time the page is built, so a test that runs the capability finds it. A tile that needs more than a few columns to stay readable — a long key, an identifier that must not be cut — declares `MinWidth` in terminal cells, so the dashboard gives it that much and no more.

**Say what the TUI may do with a result.** A list is more than a table when its rows answer keys, and those keys are yours to declare — the same way rta's own `note.list` does, with no table inside the TUI to get into:

```go
{
	ID: "acme.station.list", Summary: "List stations", Safety: plugin.Read,
	Inputs: []plugin.Field{{Name: "all", Type: plugin.Bool, Help: "closed stations too"}},
	Actions: []plugin.Action{
		{Key: "enter", Label: "show", Target: "acme.station.show", Source: plugin.ActionRow},
		{Key: "a", Label: "add", Target: "acme.station.add"},
		{Key: "x", Label: "close", Target: "acme.station.close", Source: plugin.ActionRow},
	},
	Toggles: []plugin.Toggle{{Key: "A", Label: "show closed", Input: "all"}},
	Live:    true,
	Run:     listStations,
}
```

An `Action` opens a sibling capability with the row (or, on a record's own page, the page's pairs) read into its inputs by name — `Seed` maps an input to a differently named column — and the form opens for whatever is still unfilled. `Bare` skips that form for a target that needs nothing more; it never skips a destructive target's confirmation. A `Toggle` flips one of your own `Bool` inputs and runs the view again. `Copy` names the column or key that `c` copies — a generated password, a token. `Primary` names the column or key that is the whole answer when the answer is one value, so a host writing to a pipe can print that value alone and `rta gen password | pbcopy` copies a password rather than the box drawn around it; it is its own declaration because a list can copy a column and still be a list to print whole. `Live` re-runs a `Read` view on the dashboard's interval while it is on screen. `Flash` marks a mutation whose result is a confirmation sentence, so a view that launched it shows the sentence on its footer and reloads instead of opening a page.

A capability that edits a record can declare `Prefill func(ctx, req) (map[string]any, error)`: given the record's identity — the required positional inputs — it returns the current values of the other inputs by name, and the TUI opens the edit form with them filled in, the way `note.edit` opens on today's title and body. It runs on the TUI alone, within five seconds, so it reads and never writes; the CLI and an agent pass every value explicitly and never reach it.

Validate admits all of it at registration: the keys every screen owns (`q`, `r`, `e`, `y`, `hjkl`, `b`, `/`, `?`, `tab`, `esc`) are refused, a target in your own namespace has to exist, `Bare` is refused onto a destructive target or one with a required input nothing seeds, and a target in another plugin is allowed but never bare. `rta explain <capability>` prints what you declared, and `sdktest` checks that `Copy` and `Primary` name a column or key the view really has, and that `Primary` is not one the view masks.

Name a capability for the **question it answers**, not the mechanism. `audit mail` is DNS lookups underneath, but nobody reaches for `net dns` while hardening a domain.

Give a detail page's sections an id. `view.Section` carries both an `ID` and a `Title` because they are different jobs: the title is what a person reads and should improve over time, while the id is what a script pulling one section out of your page — or an agent citing where a fact came from — addresses it by. `plugin.Page` spells them `PutAs` and `AddAs`:

```go
p.PutAs("summary", "at a glance", summary)
p.AddAs("stations", "nearby stations", listStations, plugin.Read, nil)
```

It is optional — `Put` and `Add` work, and `Key()` falls back to the title — but then rewording a heading silently renames the handle. `sdktest` says so.

**Say sizes, ages and counts the way every capability does, with `pkg/format`** rather than a copy of it, so your column reads like the one beside it: `format.Bytes(n)` for a size (`1.5 MiB`), `format.Duration(d)` for how long is left (`1h30m`, never `1h30m0s`), `format.Ago(t)` for how long ago a record's own time was (`never` when it is the zero time) and `format.Relative(t)` for an instant that is a value in its own right, `format.Clock(t)` for the moment in a sentence that says "until", and `format.CountOf(n, "entry")` for `1 entry` and `2 entries`, with `format.Count(n, "child", "children")` for a noun that does not take the rule. Zero is plural, `0 stations`, in all of them.

If your plugin **returns bytes somebody else wrote** — an object's content, a response body, a value from a store — check them with `format.PlainText` before handing them back as `view.Text`, and show the ones that are not text with `format.Dump`. Every renderer strips control characters on the way to a terminal, which is what keeps an escape sequence in the data from acting there, and also why a binary value printed as it came shows up as a blank line or a few stray letters. `format.Dump` lays the bytes out the way `hexdump -C` does, bounded to a limit you pick, and `format.Head` cuts long text without splitting a character and says how many bytes it left out. Say that count in a field of its own, beside the text, rather than appending it to the text: an unterminated escape sequence in the data swallows whatever follows it on the way to a terminal, your note included. `format.Truncate` appends the note for you and is fine for text you wrote yourself. [`pkg/format`](../../pkg/format/) is also where byte counts, durations and plurals come from, so every plugin says them the same way. A name a stranger chooses — an object key, a file, a store entry — goes in a row or a tree label through `plugin.ListedName`: shown as it is when it reads as itself, and in double quotes with each character a reader would not see written out when it does not, since a renderer that stripped the escape character from `\x1b[31mred` would show another, ordinary name.

If your plugin **grades** something — a realm's authentication settings, a repository's release hygiene — build the result with [`pkg/findings`](../../pkg/findings/) rather than a table of your own. A `findings.Report` collects one finding per check, each carrying a status, a group and the control it cites (an OWASP Top 10:2025 category with its CWE, or a named framework and control with a link to where it is read), and renders the same two ways `rta audit` does: `TableFor(req.Surface(), true)` for the compact view and the tile (`Table(true)` is the same view with every detail clipped to a line, which an agent over MCP is not given: it has no line to fit), `Page` for the sectioned detail page with the references at the end. A reader who has run one audit can read yours, and the citation discipline comes with the type: verify every reference against its primary source before shipping it, because a wrong control reads as authoritative. A check that could not run — a lookup that timed out, a file that would not parse — goes in with `AddUnchecked` rather than as a plain `Info` row: it grades nothing about the subject, and the overall then says how many checks did not run instead of "no issues found".

## Publishing it

An **index** is a git repository holding one `index/<name>.yaml` manifest per plugin. That is the entire format. rta clones it with your own `git`, so your remotes, proxies and credentials keep working — over `https`, `ssh`, or a path on this machine; a `<transport>::<argument>` remote helper and cleartext `http://`/`git://` are refused — and it answers `rta plugin search` from the manifests alone without fetching or running anything.

```
my-index/
└── index/
    ├── mytool.yaml
    └── othertool.yaml
```

**Do not write the manifest by hand.** Every claim in one is already in your declaration: the name, the version, the summary, each capability's ID, safety class and grant flag, and each credential location the plugin asks for. `rta plugin install` derives all of it again from the bytes it downloads and refuses the install when the two disagree — so a transcription slip does not surface for you, it surfaces on somebody else's machine as a message about an index they cannot fix.

Generate it instead:

```bash
rta plugin manifest bin/rta-plugin-mytool \
  --version v1.2.0 \
  --homepage https://github.com/you/rta-plugin-mytool \
  --checksums dist/checksums.txt \
  --platform linux/amd64=https://github.com/you/rta-plugin-mytool/releases/download/v1.2.0/mytool_linux_amd64.tar.gz \
  --platform darwin/arm64=https://github.com/you/rta-plugin-mytool/releases/download/v1.2.0/mytool_darwin_arm64.tar.gz \
  --index ../my-index
```

rta runs your binary the way a load does — sandboxed — and writes down what it declares. You supply the one thing the binary cannot know: where its bytes will live. `--checksums` reads the `<sha256>  <filename>` lines your release already publishes and matches them by filename; a `--platform` pointing at a file on your machine is hashed on the spot instead, and its archive is opened to prove the `bin:` claim while it is still in reach. `--bin` says where the binary sits inside a `.tar.gz` when it is not at the top under its usual name, `rta-plugin-<name>`.

The reference page is generated the same way, for the same reason — a page written by hand goes stale on the first commit that touches a capability, and nobody notices, because the commit is about something else:

```bash
rta plugin doc bin/rta-plugin-mytool > README.md
```

One markdown page from the declaration: the capability table, every config key the plugin reads and which capabilities read it, and one section per capability holding the same card `rta explain` prints. Regenerate it in CI and fail when the committed copy differs, and the page cannot lie about the binary beside it.

Two things to know before you publish:

- **`.tar.gz`, or a bare binary.** rta extracts a single member from a gzipped tar and has no zip reader at all, so a `.zip` artifact cannot be installed. `rta plugin manifest` refuses one rather than letting it become a failed install somewhere else.

- **A registry works too, and needs no checksums file.** `--platform linux/amd64=oci://ghcr.io/you/rta-plugin-mytool:1.2.0-linux-amd64` names an OCI artifact — one layer, pushed with `oras push` or anything that speaks the distribution spec. rta reads the digest and the media type from the registry that will serve the bytes, which is a better source than a file sitting beside your build, so `--checksums` is for `https://` artifacts only.

  Pulls are **anonymous**: rta sends no credential to any registry, so the artifact has to be publicly readable. A package on ghcr is private until you make it public, and until you do, install refuses with *"ghcr.io will not serve … anonymously"*. That refusal is deliberate rather than a gap — an index is somebody else's repository, and authenticating to whatever host a manifest names would turn a search result into a way to spend your credentials.
- **Stamp the version at build time, don't type it.** A version is a fact about a release, and a literal in your source is a fact about nothing — it stays at `0.1.0` through every release you cut, because bumping it is a step nobody's build performs. `rta plugin new` scaffolds the pattern instead: a `var version = "dev"` the declaration reads, set by the linker.

  ```bash
  go build -ldflags "-X main.version=$(git describe --tags)" .
  ```

  That is GoReleaser's own default ldflag, so a release stamps it for free, and an unstamped build says `dev` rather than claiming a version that was never cut. `--version` still overrides, for the case where the tag and the artifact genuinely disagree.

Then attach your own index and take the round trip yourself, before anybody else does:

```bash
rta plugin index add mine /path/to/my-index
rta plugin search mytool
rta plugin install mine/mytool
```

Install is where claims meet evidence: rta fetches the artifact, hashes it, launches it in the same sandbox any load uses, and refuses if what it declares is not what your index said — naming your index. Earning that refusal on your own machine is the point of running it.

## Related

- [Using plugins](./10-plugins.md) — how an operator finds, trusts and installs what you publish

## Next

[Start here](../01-readme.md) — pick another track.
