# Writing an rta plugin

A plugin is a program that returns a declaration. rta launches it, asks what it
can do, and renders that on four surfaces — CLI, TUI, MCP and JSON — none of
which your code mentions.

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

That is the whole loop. The mechanical part takes about two seconds; the rest
is deciding what your capability should do.

**The `trust` step is not paperwork.** rta loads a plugin by *running* it —
that is how it learns what you declared — so a file called `rta-plugin-*` on
`$PATH` would execute before anybody typed a command naming it, including the
`rta __complete` a tab press runs. Being on `$PATH` is not consent, so an
artifact runs once somebody has approved that exact digest. Rebuild and it
needs approving again, which during development is a keystroke and in
production is the event worth stopping for. `rta plugin trust` on its own lists
what is waiting.

Your inner loop does not need it at all: `rta plugin dev` compiles from a
directory you named in the command you just typed, which is a stronger act of
approval than a digest in a file, so it is exempt.

**Your binary's name is your namespace.** `rta-plugin-weather` declares
`Name: "weather"`, and rta refuses it otherwise — the name an operator gave
the file by installing it wins over the name the file gives itself, because
anything on `$PATH` can claim to be anything — the same reason the artifact
needs trusting before it runs at all. `rta plugin new` gets this right for you;
`rta plugin dev` is exempt from both, so your inner loop does not care what the
temporary binary is called.

> **Before rta is published**, a scaffolded plugin needs a `replace` directive
> pointing at your rta checkout. `rta plugin new` adds one automatically when it
> can find one by walking up from your working directory or from the rta binary;
> otherwise pass `--rta-source <path>`. It tells you which happened.

## The one thing to understand

You return **data**, not output.

```go
func greet(_ context.Context, req plugin.Request) (view.View, error) {
	return view.Text{Body: "Hello, " + req.String("name") + "!"}, nil
}
```

`view.Text`, `view.Table`, `view.KeyValue`, `view.Tree`, `view.Chart` and
`view.Sections` are the whole union. A `view.Table` becomes a bordered table in
a terminal, a navigable list in the TUI, CSV under `-o csv`, a markdown table
under `-o md`, and structured JSON to an AI agent. You write it once.

The same applies to failure. Return a `view.Error` rather than a bare error
where you can:

```go
return nil, view.Errorf("weather.nosuchcity", "no station for %q", city).
	WithHint("try `weather stations` for the ones that exist")
```

The code is stable enough for a script to branch on, and the hint is what the
person does next. Both are lost by `fmt.Errorf`.

## Declaring inputs

```go
Inputs: []plugin.Field{
	{Name: "city", Type: plugin.String, Positional: true, Required: true, Help: "which city"},
	{Name: "units", Type: plugin.String, Default: "c", Options: []string{"c", "f"}},
	{Name: "key", Type: plugin.Secret, Help: "API key"},
	{Name: "out", Type: plugin.Path, Local: true, Help: "write the report here"},
}
```

`Type` is mandatory and closed — there is no inference from the name, because
`Secret` is what makes a value masked and `Path` is what makes it completable,
and neither is guessable ([ADR 0011](adr/0011-field-type-closed-and-mandatory.md)).

What each one buys you:

- **`Options`** becomes a TUI picker, shell completion, and an `enum` in the MCP
  schema. Use it for a fixed set.
- **`Suggest`** is a function returning what exists *right now* — your tags,
  their hostnames. It runs on human surfaces only, never for an agent: the list
  itself is information. It must be cheap and silent on failure, because it
  fires on a keystroke — no network call, no prompt, no connection opened.
  It receives what the caller has supplied so far, on the CLI and in a TUI form
  alike, so a suggestion can depend on a sibling field being typed above it.
  Tab completes it on both surfaces. Not accepted on a `Secret` or a `Text`
  input: the list renders in plain text beside the box, which defeats a mask,
  and a body is written in `$EDITOR` rather than completed.
- **`Local: true`** means the value names something on *this* machine, so it is
  refused over MCP. `--out` is the example: a grant authorises revealing a
  value, not choosing where on the operator's disk it lands.
- **`Path`** inputs are confined to the server's roots when an agent supplies
  them ([ADR 0014](adr/0014-path-confinement-at-the-mcp-boundary.md)).
- **`Config`** names a key in the operator's configuration this input may be
  filled from when nobody passed it, so a connection is stated once instead of
  retyped. Precedence is caller, then config, then `Default`, and your handler
  cannot tell which it got.

```go
{Name: "host", Type: plugin.String, Required: true, Config: "host"},
{Name: "port", Type: plugin.Int, Default: 5432, Config: "port"},
{Name: "mode", Type: plugin.String, Default: "prefer", Config: "tls.mode"},
```

The operator writes it under your plugin's own section, which is pinned to the
binary they installed rather than to the namespace you declare — anything on
`$PATH` can claim a namespace, and their stated values must not go to whoever
won that race:

```yaml
plugins:
  weather@1a2b3c4d5e6f:
    host: db.internal
```

`rta doctor` prints the exact line, digest included, and says so again if you
upgrade and the pin goes stale. **`Config` is refused on a `Secret` input** —
configuration is a plaintext file read on every invocation, and a `Secret`'s
default is published in your MCP tool schema. Use `Local: true` and let the
host resolve it from its own environment ([ADR 0016](adr/0016-plugin-config-is-a-declared-input-pinned-to-the-artifact.md)).

## Safety is a claim, not a label

Every capability declares exactly one of `plugin.Read`, `plugin.Write` or
`plugin.Destructive` — it is one value, not a set of flags.

It decides what an AI agent can reach without a human, so it is a statement
about **blast radius** rather than about whether you touch the disk:

| Class | Meaning | What an agent needs |
|---|---|---|
| `Read` | changes nothing, reveals nothing sensitive | nothing |
| `Write` | changes something, **or reveals a secret** | `--allow-write <your-plugin>` |
| `Destructive` | removes something with no undo | an explicit per-capability allowlist **and** a grant a person issued |

The rule that catches people: **a capability that reveals a secret's plaintext
is `Write`, even though it mutates nothing.** `kv get` is the canonical case.
If yours prints a token, signs with a private key, or dumps an environment,
it is not `Read`.

Set `NeedsGrant: true` when the class understates it, and `Scope: "city"` to
name the input a grant can be narrowed to — then a person can allow one record
rather than the capability.

`rta explain <capability>` prints the exact flag an operator would need. So
does `rta plugin dev`, in its `Agents` column, which is the fastest way to
check you classified something the way you meant to.

## What rta does to your process

Worth knowing before you debug something surprising:

- **Your stdin is `/dev/null`.** The protocol owns the real one. Never prompt —
  declare a `plugin.Secret` input and let the surface ask.
- **On macOS you are sandboxed.** You cannot read or write rta's own config and
  data directories, and cannot read `~/.ssh`, `~/.aws`, `~/.kube` and the rest.
  Everything else is readable. `rta doctor` prints the set. Linux and Windows
  are **not** confined and say so.
- **Your environment is filtered** to `PATH`, `HOME`, `TMPDIR`, `TZ`, `LANG`,
  `LC_*` and the TLS cert variables. No `RTA_*`, no cloud credentials, nothing
  your user exported. If you need a value, take it as an input.
- **You are in your own process group** and everything you spawn dies with you.
- **One process serves every call**, started on the first one and reused. Do not
  assume a fresh process per capability, and do not hold per-call state in a
  global.
- **A panic in a handler is caught** and returned as an error naming your
  capability. It does not take the process down, so the other capabilities keep
  working — but it is still a bug and it still says your name.

## Declared text is checked

Your `Summary`, `Description`, `Help` and `Options` are published verbatim to AI
agents as tool descriptions. rta refuses control characters, bidirectional
overrides, invisible characters and its own framing markers at registration, and
caps the lengths ([ADR 0013](adr/0013-the-agent-facing-text-channel.md)). If
`rta plugin dev` refuses your plugin over a summary, that is why.

Write the `Description` for somebody deciding whether to call it. It is the text
a model reads before choosing.

## Testing

`rta plugin new` ships a `main_test.go` wired to `pkg/sdk/sdktest`, so `go
test` passes from the first minute:

```go
func TestPlugin(t *testing.T) { sdktest.Check(t, Plugin()) }
```

It runs the catalogue-wide invariants rta holds its own built-ins to — the
shared verb vocabulary, every declared view rendering in every format it claims,
dry-run honesty on anything that writes. It found a built-in sending real bytes
on `--dry-run` the first time it was pointed at rta's own catalogue.

## Conventions worth following

Use the shared verb vocabulary. `sdktest` warns on a novel verb, and when your
word has a standard spelling it names it — it will tell you rta writes `delete`
as `rm`, in four places already. The whole list:

```
add  done  edit  get  init  inspect  list  overview
reopen  rm  search  set  show  status  tags  toggle
```

`sdktest.Vocabulary()` returns the same list at runtime, so you never have to
trust this page over the tool. The point is that learning one plugin teaches
you all of them.

One of those words carries extra weight. **`<your-plugin>.overview` becomes
your dashboard tile** — the panel the TUI draws for your plugin on its landing
screen. Without one, the tile is whichever of your capabilities happens to come
first and can run unattended, which means your declaration order picks it by
accident. Declaring an overview is how you pick it on purpose.

A tile runs on load and then every few seconds with nobody watching, so it has
to be `Read`, answerable from its defaults alone, and cheap enough to repeat.
If your overview is none of those, set `NoPreview: true` on it — rta will tile
something else rather than put it on a timer, and `overview` still means to a
reader what it means everywhere else.

Name a capability for the **question it answers**, not the mechanism. `audit
mail` is DNS lookups underneath, but nobody reaches for `net dns` while
hardening a domain.

Give a detail page's sections an id. `view.Section` carries both an `ID` and a
`Title` because they are different jobs: the title is what a person reads and
should improve over time, while the id is what a script pulling one section
out of your page — or an agent citing where a fact came from — addresses it
by. `plugin.Page` spells them `PutAs` and `AddAs`:

```go
p.PutAs("summary", "at a glance", summary)
p.AddAs("stations", "nearby stations", listStations, nil)
```

It is optional — `Put` and `Add` work, and `Key()` falls back to the title — but
then rewording a heading silently renames the handle. `sdktest` says so.

## Reference

- [`examples/plugin-hello`](../examples/plugin-hello/main.go) — a complete
  two-capability plugin, and the fixture rta's own host tests run against.
- [ADR 0012](adr/0012-plugin-confinement-and-process-lifetime.md) — confinement
  and process lifetime, including what it does *not* protect against.
- [PROJECT.md §4.3](../PROJECT.md) — the view contract in full.
