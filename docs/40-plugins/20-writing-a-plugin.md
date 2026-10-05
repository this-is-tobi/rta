# Writing an rta plugin

A plugin is a program that returns a declaration. rta launches it, asks what it can do, and renders that on three surfaces — CLI, TUI and [MCP](../95-reference/10-glossary.md#acronyms), with JSON, CSV and markdown as formats the CLI answers in — none of which your code mentions.

## Fifteen minutes, start to finish

```
rta plugin new weather        # a plugin that already builds and runs
cd rta-plugin-weather
rta plugin dev                # build it, and see what rta sees
rta plugin dev -- weather greet world
go build -o ~/.local/bin/rta-plugin-weather .
rta plugin trust weather
rta weather greet world
```

That is the whole loop. The mechanical part takes about two seconds; the rest is deciding what your capability should do. `rta plugin dev` builds into a temporary directory and removes it when it exits; `--keep` leaves the binary in place and prints where, for running it by hand or putting a debugger on it.

**The `trust` step is not paperwork.** rta loads a plugin by *running* it — that is how it learns what you declared — so a file called `rta-plugin-*` on `$PATH` would execute before anybody typed a command naming it, including the `rta __complete` a tab press runs. Being on `$PATH` is not consent, so an artifact runs once somebody has approved that exact digest. Rebuild and it needs approving again, which during development is a keystroke and in production is the event worth stopping for. `rta plugin trust` on its own lists what is waiting.

Your inner loop does not need it at all: `rta plugin dev` compiles from a directory you named in the command you just typed, which is a stronger act of approval than a digest in a file, so it is exempt.

**Your binary's name is your namespace.** `rta-plugin-weather` declares `Name: "weather"`, and rta refuses it otherwise — the name an operator gave the file by installing it wins over the name the file gives itself, because anything on `$PATH` can claim to be anything — the same reason the artifact needs trusting before it runs at all. `rta plugin new` gets this right for you, and refuses a name a plugin on this machine already answers to — a built-in's or an installed one's — since a namespace is one plugin's alone; `rta plugin dev` is exempt from trust and from the filename, so your inner loop does not care what the temporary binary is called.

> A scaffolded plugin requires the released rta module at the version of the rta that scaffolded it, so `go mod tidy` resolves `pkg/sdk` from the module proxy like any other dependency and the SDK it builds against is the one whose host will load it. Inside an rta checkout — or anywhere, with `--rta-source <path>` — `rta plugin new` points a `replace` at the tree instead, for building against unreleased changes. Its answer says which happened, on its `builds against` line — under `-o json` too, beside the directory it wrote and the files in it.

## What `plugin new` wrote

The Go module is named for the binary, `rta-plugin-weather`, which builds anywhere; `--module github.com/you/rta-plugin-weather` names it for the repository you will publish from, and `--dir` says where the files go. A plugin is an ordinary Go `main` package, and the whole of its contract with rta is the value it serves:

```go
func main() { sdk.Serve(Plugin()) }

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:         "weather",
		Summary:      "what the plugin is for, in one line",
		Version:      version,
		Capabilities: []plugin.Capability{ /* one per thing it can do */ },
	}
}
```

`sdk.Serve` is imported from `pkg/sdk`, the declaration types from `pkg/plugin`, and the answers from `pkg/view`. `Name` is the namespace and the first word of every capability ID (`weather.greet`), `Summary` is the line `rta plugin list` shows, and `Version` is what the build claims to be — shown by `rta plugin dev` and recorded by an index entry. Stamp it from your release, `go build -ldflags "-X main.version=$(git describe --tags)" .`, rather than editing a constant: an unstamped build says `dev` instead of a version nobody cut. `Needs` is the one other `plugin.Plugin` field, and [If your plugin needs a credential location](./23-safety-and-credentials.md#if-your-plugin-needs-a-credential-location) says when to set it.

## The one thing to understand

You return **data**, not output.

```go
func greet(_ context.Context, req plugin.Request) (view.View, error) {
	return view.Text{Body: "Hello, " + req.String("name") + "!"}, nil
}
```

`view.Text`, `view.Table`, `view.KeyValue`, `view.Tree`, `view.Chart` and `view.Sections` are the whole union. A `view.Table` becomes a bordered table in a terminal, a navigable list in the TUI, CSV under `-o csv`, a markdown table under `-o md`, and structured JSON to an AI agent. You write it once. A table whose rows are in time order says so with `Tail: true`: the newest row is last, the terminal ends on it, and the TUI opens on it — a log is read from where things are now, back. A listing with nothing in it is still its table, with no rows, and says so with `Empty`: the sentence a person reads in place of headings over nothing, like what would fill it. A terminal, `-o md` and the TUI draw it; `-o json`, csv, a pipe and an agent get the empty table, so a script never meets a sentence where it expects rows. `view.Text`, `view.Tree` and `view.Sections` carry the same field for an answer that is empty text, a tree with no roots or a page with no sections.

A table is the shape most listings take, and it is a value like any other:

```go
return view.Table{
	Columns: []view.Column{{Name: "Station"}, {Name: "Region"}},
	Rows:    [][]string{{"alpha", "north"}, {"beta", "south"}},
	Total:   2,
	Empty:   "no stations yet — " + req.Surface().CapabilityName("stations.add") + " adds one",
}, nil
```

Cells are strings and a column's `Kind` (`view.KindStatus`, `view.KindDuration`, …) is a hint the renderers style by, never styling itself. `Total` is how many rows the answer holds when the page shows fewer.

A table that stops short hands back where it left off: `Page: &view.Cursor{Next: next, Input: "after"}`. `Next` is the value, and `Input` names the input of the same capability that takes it, so whoever reads the table is told what to pass instead of left to guess. It has to be one an agent can give: a declared `String` that is not `Local`, which `sdktest` checks; a cursor that names no input still works for whoever knows the plugin, and `sdktest` notes it.

The other views are built the same way:

```go
view.Text{Body: "# Tides\n…", Markdown: true}
view.KeyValue{Pairs: []view.Pair{{Key: "host", Value: "tides.example"}, {Key: "token", Value: token}}, Redacted: []string{"token"}}
view.Tree{Roots: []view.Node{{Label: "north", Detail: "2 stations", Children: []view.Node{{Label: "alpha"}, {Label: "gamma"}}}}}
view.Chart{Kind: view.ChartBar, Series: []view.Series{{Name: "alpha", Points: []float64{3.2}}}, Unit: "m", Max: 5}
view.Sections{Items: []view.Section{{ID: "summary", Title: "At a glance", View: summary}}}
```

A `KeyValue` is one thing's facts, a `Tree` is labelled structure whose labels are names a person reads (paths, hosts, subjects), and a `Sections` page holds any of the others under titles. A `ChartBar` series is one labelled value, its first point, and a `ChartLine` series is a sequence; `Max` fixes the top of the scale (`100` for a percentage) so an idle series is not drawn at full height, and `Unit` annotates the values. An error you built with `view.Errorf` has a `Retryable` field to set when the same call may succeed if it is repeated — a lost connection rather than a bad argument — and an agent reads it in the JSON.

**A secret in an answer is a field, and you name it.** A `view.KeyValue` lists the keys, and a `view.Table` the columns, whose values must not be shown in `Redacted` — `Redacted: []string{"token"}` — and the host masks them on every surface: the terminal, the TUI, every `-o` format and an agent's result alike, and still once the view is embedded in a page. Spell the name as the view has it: a name the view does not contain protects nothing, and `sdktest` fails it. `view.Text`, `view.Tree` and `view.Chart` cannot carry the declaration, so a secret never goes in one — it is a field of a `KeyValue` or a `Table`, or it is not returned. `sdktest` also asks, once, of a capability that takes a `Secret` or needs a grant and returns a `KeyValue` or `Table` with nothing marked, and the answer is one of three: mark the field, declare that this capability is the reveal (`Reveals: true`, below), or say it shows no secret with `sdktest.Skip(sdktest.RuleRedaction, "acme.thing.list", "names only")`.

**A mask points at where the value can be read.** A view that masks a value and says nowhere to read it is a dead end: nobody can tell whether the value is out of reach by design or only out of this page. Put a pair keyed `view.RevealKey` in the view, whose value is the call that returns the value, spelled for the reader with `req.Surface().Call("acme.thing.get", plugin.Arg{Name: "id", Value: id, Positional: true})`; a `Table` has no pair of its own, so it sits in a `Sections` page beside a `KeyValue` that holds it. `sdktest` fails a masked view with no such pair. A mask that is permanent by design — the credentials rta itself connects with, which the operator already holds — is waived with `sdktest.Skip(sdktest.RuleRedaction, "acme.config.get", "the credential acme connects with")`, and the reason is printed on every run.

The same applies to failure. Return a `view.Error` rather than a bare error where you can:

```go
return nil, view.Errorf("weather.nosuchcity", "no station for %q", city).
	WithHint(req.Surface().CapabilityName("weather.stations") + " lists the ones that exist")
```

The code is stable enough for a script to branch on, and the hint is what the person does next. Both are lost by `fmt.Errorf`.

This is the shape of every answer a plugin gives. [Declaring inputs](./21-declaring-inputs.md) is what it takes, [Answers, errors and hints](./22-answers-errors-and-hints.md) is everything else an answer can carry, and [Safety, credentials and what rta does to your process](./23-safety-and-credentials.md) is the part that decides what an agent may do with it.

## Worked examples, in the order worth reading them

Twelve first-party plugins live in [rta-plugins](https://github.com/this-is-tobi/rta-plugins), and they are not a showcase — they are the proof the contract works, written against the same public SDK you have, from the same released rta. Every one is a separate module that cannot reach into rta's internals, so anything they do, you can do.

Read them in this order and each one adds exactly one idea:

| Plugin | Read it for |
| --- | --- |
| [`examples/plugin-hello`](../../examples/plugin-hello/main.go) | The whole shape in one file — and the fixture rta's own host tests run against |
| [`builtin/eol`](../../builtin/eol/eol.go) | The smallest real one: a single capability over a public API, nothing to configure — built in, and the same declaration a plugin would make |
| [`kube`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/kube) | Shelling out to a tool the operator already has (`kubectl`) instead of linking its client library, and why |
| [`cnpg`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/cnpg) | The opposite choice from `kube`: one plain API read against a Custom Resource instead of a shell-out, and declaring a credential [`Need`](./23-safety-and-credentials.md#if-your-plugin-needs-a-credential-location) rather than assuming one. Also a single `Write` among reads, and what it costs to add one |
| [`mysql`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/mysql) | A connection: declared inputs, a `Secret` a profile fills, an endpoint role a tunnel can fill |
| [`mariadb`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/mariadb) | Two plugins over one service family without either becoming a fork of the other |
| [`pg`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/pg) | Safety classes doing real work — three dumps graded by what a grant can name, two refusing MCP outright |
| [`s3`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/s3) | `Live` completion from the service itself, and a download that refuses any object key landing outside the directory you named |
| [`vault`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/vault) | A plugin where almost everything is a secret, and what that does to every declaration |
| [`etcd`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/etcd) · [`qdrant`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/qdrant) | Tree views, and a plugin whose whole subject is a keyspace |
| [`docker`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/docker) | A local daemon socket rather than a network endpoint |
| [`redis`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/redis) | Speaking a wire protocol in-package when the client library would triple the binary |
| [`keycloak`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/keycloak) | A plugin whose whole reason to exist is an audit: `pkg/findings` from a plugin, a representation decoded into a shape that has no field for the secret it carries, and a credential exchanged for a five-minute token on every call |

`rta plugin new <name>` scaffolds one that builds, passes its conformance suite and runs, so none of these is where you start — they are where you look when your plugin needs the thing they already do.

## Reference

- [`rta explain <capability>`](../20-using/10-cli.md#rta-explain) — the authoritative card for any capability, generated from the declaration itself. The fastest way to check what rta made of yours.
- [`pkg/plugin`](../../pkg/plugin/) and [`pkg/view`](../../pkg/view/) — the contract in code, with the reasoning in the doc comments.
- [`pkg/format`](../../pkg/format/) — byte counts, durations, plurals, and bytes that may not be text, said the way every other capability says them.
- [`pkg/findings`](../../pkg/findings/) — the graded-check report an audit returns, rendered the way `rta audit` renders its own.
- [`pkg/sdk/sdktest`](../../pkg/sdk/sdktest/) — the conformance suite your plugin should pass.
- [`pkg/sdk/spelling`](../../pkg/sdk/spelling/) — the speller the suite holds your declared text to, for any other text your tests read.

## Next

[Declaring inputs](./21-declaring-inputs.md) — what a capability takes, and how a connection is declared once for every capability of a plugin.
