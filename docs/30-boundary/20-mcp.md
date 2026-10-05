# MCP and the safety gate

rta speaks the Model Context Protocol over stdio by default, and over HTTP with `--http` — see [Hosting a server](./65-hosting-a-server.md). An MCP client launches `rta mcp serve` and gets every capability as a tool, with typed schemas, safety annotations and structured results. [Connect an agent](../10-getting-started/30-connect-an-agent.md) is how to wire one up; this chapter is what it can reach once it is, and what every call is checked against.

The interesting part is not that it works. It is what an agent can reach before you have decided anything.

Everything in this chapter assumes the agent goes through the server. An agent that can also run shell commands can run `rta` itself, and [what that does to the guarantees](./10-the-boundary.md) is worth reading first.

## What is exposed, before you decide anything

**Only reads, and only the ones that stay on this machine.** That is the default, and it holds with no flags, no config and no decisions.

| Safety class | What an agent needs |
| --- | --- |
| `read` | nothing, unless it names a destination (below) |
| `write` | a grant a person issued |
| `destructive` | a grant a person issued |

Ten reads name a destination the agent chooses, and a destination is a request rta makes on its behalf: `net.dns`, `net.ping`, `net.port`, `net.probe`, `net.trace`, `http.get`, `http.head`, `cert.expiry`, `audit.web` and `audit.mail`. They cost a grant like a write, and the grant can name the one host. `rta explain` shows `grant required (mcp)` on each card, and so on any capability run against a configured connection.

```bash
rta grant allow note --agent claude --ttl 30m   # every write in the note plugin, for half an hour
rta grant allow note.rm --agent claude --ttl 5m # one destructive capability, for five minutes
```

**One gate, and a grant is it.** `rta mcp serve` has no switch that allows writes or destructive calls for the life of the server. A call that changes anything is decided when it is made, by a grant, so one vocabulary answers one question and the grant you issued is the thing that decides.

That is stronger than a standing allowlist, and it is worth being plain about why. A flag typed into a client's config file months earlier would reach a write for a whole server's lifetime, with nothing per call and no expiry, and a standing allowlist is precisely what consent should not be. A write costs a grant a person issued, which [the guard](./30-grants.md#the-guard-a-passphrase-in-front-of-issuance) can price, a [team ceiling](./50-team-policy.md) can cap, [the record](./40-audit-trail.md) shows being spent, and the clock takes back.

An agent can *see* every capability that is not [reserved for you](#never-a-tool), and call none that changes anything. That is deliberate: a tool it can see and is refused produces a refusal naming the exact command you would run, and a tool that is simply absent produces a model guessing at a different spelling.

A grant on a plugin's capability binds to that plugin's **artifact**, not to its name: it records the digest of the binary it was issued against. Replace the binary behind a plugin and the grants standing on it stop covering anything. Built-ins have no separate artifact to pin — the rta binary you chose to run is the artifact — so their grants carry no digest. `rta grant list --detail` shows the build each grant is bound to, and marks a grant whose plugin has been replaced since it was issued, with the fix.

### Never a tool

A few capabilities are not on offer at any price. `grant`, `agent`, `lock`, `operator` and `pkg` — every verb in each — plus `audit clients`, `audit doctor`, `kv copy`, `kv edit` and the `keys` verbs that move key material answer to the person at the terminal and to nobody else. They are absent from `tools/list` on every transport, whatever the flags: an agent that could issue itself a grant, lift its own lock, or read the roster of what your other agents may do would make the rest of this chapter theatre. A call naming one anyway is answered as an unknown tool and written to [the record](./40-audit-trail.md) like any other probe. `rta explain` lists them under *never a tool*.

## Paths and roots

Every path argument a call would use must sit under a **root** — one the agent sent, a capability's declared default, or one your config names. The default root is the directory the server was started in; widen it with `--root`, which is repeatable, in the registration — the client launches the server, so a flag that is not in that line is not set:

```bash
rta mcp install claude --root ~/projects --root /tmp/scratch
```

`rta mcp serve --root` is the same flag for a server you start yourself.

The gate has a great deal to say about links, `git`, hard links and what rta withholds as its own; all of it is on [The path gate](../95-reference/20-the-path-gate.md). What you need day to day is that rta says its roots out loud at startup rather than leaving them to be discovered from a refusal:

```
rta mcp server listening on stdio
path arguments confined to: /Users/you/projects, /tmp/scratch
record: /Users/you/.local/share/rta/agent-log.jsonl (session 4a030411)
```

The `record:` line names the file this server's calls are written to and the id of its session, which is what `rta agent log --session` takes; when the record looks empty, it is the path to compare with the one `rta agent overview --detail` reads. A root the server cannot open for reading is named there too, once, with the fix — one whose mode lets it be searched and not listed (`--x`), or a folder macOS keeps from the app that started the server. The built-ins open a path from its root, so they refuse every path they would open through it, on every call, until it can be read; the server serves the other roots meanwhile.

## What a call is held to

### What an argument is held to

Before any gate, every argument is checked against the tool's published schema and the capability's declaration. A value of the wrong shape, or an argument the tool does not have, is `core.mcp.badargs` — and an input that only the operator sets, such as the server a call is aimed at or a credential, is an argument the tool does not have to a caller: it is refused in the same words as a typo, with the same list of what the tool does take, so nothing in the refusal says whether the input exists. A value of the right shape the declaration does not take is refused with the code a person at the CLI gets for the same mistake — `core.input.option` for a value none of its options name, `core.input.range` for a number outside its range, `core.input.missing` for a required argument left out or sent empty — and, being refused first, it spends no grant and asks you nothing. An input the CLI reads from a pipe when it is left out, such as the token `codec jwt` decodes, is required here, because an agent has no pipe to give.

An agent's call runs with your config and a profile's `set:` exactly as yours does: what it sends beats them, and what it leaves out comes from them before a declared default. The grant gate, the consent prompt, the path gate and [the record](./40-audit-trail.md) all see the values the call runs with, so a value your config sets that the CLI would refuse is refused here too, in words that say it is yours to change. Two required inputs are looked for after the gate instead, because nothing can have filled them before it: one the named profile may set, since the profile is read only once a grant allows it, and one only you can give, which the tool's schema does not show. Missing then, the call is refused as `core.input.missing` and the use it took is given back. A required input your config gives a value is not listed as required in the tool's schema, so a client that checks a call against the schema before sending it lets through one that leaves it out; an input only a profile sets stays listed, since a call chooses its profile and the schema is the same for every call.

A config that does not read when the server starts does not stop it, and does not leave the base connection open either. With no record of which plugins have profiles, a call that names none cannot be told from one that must, so every capability a profile could change is refused as `core.profile.unreadable` — with `rta doctor` as the hint and no path in the message — while the rest keep working, and the server says why on its stderr. The first read of the file that succeeds lifts it, without a restart. A file that read at startup and fails later is answered from what was read.

### How large a result may be

A result is held whole while it is handled, and measured at about sixteen times its size: a plugin that answered with 100 MB took the server to 1.68 gigabytes. So a result is bounded, by default at 8 MiB, which is more than anything the catalogue answers honestly (an HTTP body is cut at 1 MiB, a listing runs to hundreds of kilobytes, a table dump of a few thousand rows to a few megabytes) and more than a model can use. A plugin's answer is refused while it is received, from the length its message declares, before any of it is held; a built-in's is measured as it is sent. Either way the call ran, what it changed is changed, and the agent is told it as `core.result.toolarge` with the size, the limit and how to ask for less — a smaller limit, a tighter filter, a narrower path. The record keeps the call as one that ran, with that code beside it.

`--max-result <MiB>` sets the ceiling, between 1 and 256, on `rta mcp serve` and on `rta mcp install`, which writes it into the registration. It is the operator's: an agent has no argument that raises it. The CLI and the TUI are not bounded by it, since the person at them chose to ask. Size the server's memory for what it allows: the default is about 128 MiB at its peak for one answer that large, several at once add up, and a pod's limit is the place the ceiling is really set — [the chart](./80-kubernetes.md) passes `--max-result` through `serverDefaults.extraArgs`.

### How large a request may be

A request is held whole while it is read, and a single argument of 100 MB took the server to 2 gigabytes before any tool, gate or schema had seen it. Over HTTP a request body past 4 MiB has always been refused; over stdio the stream is held to the same size now, followed as JSON is structured so that a message cannot be made small by spreading it across lines. A message past it ends the session, with the limit named on the server's standard error, because its id cannot be known without holding it and a request that is dropped would be a call its client waits on for good. No model writes four mebibytes of arguments to one call, and a script that means to sends them as the smaller calls they are.

### What an error or a result tells an agent

An error is written for a person at a terminal and says where: `reading /home/you/.local/share/rta/kv.age: permission denied`. Over MCP that would hand an agent the layout of the place rta keeps what it must not read or move, so every error an agent is handed names rta's data directory as `<data dir>`, its config directory as `<config dir>` and the file `RTA_KV_IDENTITY` names as `<identity file>`, wherever in the message or the hint it appeared, the operating system's own text included. A path the agent sent is its own and is left as it was. The record keeps the error as it was written, since it is yours. A result is named the same way, `kv_status` included: where the store is is for the person who runs rta, and an agent is told it as `<data dir>/kv.age`. A place is replaced only where it is a path of its own, so a data directory called `/data` does not rewrite `/database` or `/srv/data/x`. The URL userinfo, token parameters and credential headers an error would repeat are masked the same way as in the record.

### What an agent is told

A tool's description is the capability's own summary and description inside a frame that says those words are the plugin's, followed by rta's: its safety class, and the grant it needs and whom to ask for one. What is true of every tool is said once instead, in the instructions the server returns at the handshake: a result is one JSON object whose `type` names its shape, a table's `total` counts every row there is and more than it sent means the list was cut, a failure carries a `code`, a `message` and a `hint`, a command in a hint is yours to run and an agent has no terminal for it, and a path is read on the machine running rta under the roots you started it with. A result is sent once, as that JSON text, with no second copy as structured content, so a client that forwards every field to its model does not pay for each row twice. A result is also held to 80 KiB, which is about what fits beside a task in a model's context and under the ceiling some clients put on a tool result: a table past it is cut to its first rows (its last, for a log) with its true `total` and a `core.mcp.result.cut` warning, a text or a response body ends on a line saying how much was left out, and a tree or a chart that cannot be cut is refused as `core.mcp.result.toolarge`, so a model is told to narrow the call rather than handed half an answer or a refusal from its client that names nothing to change.

### One gate

[Grants](./30-grants.md) are the whole of it: consent for one capability or one plugin, optionally one record, narrowed to one agent and one connection, expiring on its own. `rta grant allow note --ttl 8h` is the shape for "this agent works on notes today"; `rta grant allow kv.get deploy-key --ttl 5m --max-uses 1` is the shape for "this once".

## The rest of MCP

This chapter is what a connected agent can reach and what every call is held to. The rest of the subject is on its own pages:

| If you came for… | Read |
| --- | --- |
| Registering a client, naming the agent | [Connect an agent](../10-getting-started/30-connect-an-agent.md) |
| Live consent, and how a grant is bound to a task | [Grants](./30-grants.md#live-consent-when-you-would-rather-be-asked) |
| The kv store opening from the environment a server inherits | [Secrets](../20-using/50-secrets.md#what-this-means-for-agents) |
| The working directory a client chooses, and the team policy | [Team policy](./50-team-policy.md#where-an-mcp-server-looks-and-why-it-may-surprise-you) |
| Locks: the instant no | [Stop an agent now](./45-stop-an-agent-now.md) |
| `--http`, tokens, probes, what a remote server hides | [Hosting a server](./65-hosting-a-server.md) |
| The operator channel | [The operator channel](./66-operators.md) |
| The container recipe, sharing an image, why not one server | [Containers and images](./67-containers-and-images.md) |
| Symbolic links, `git`, hard links: the whole path gate | [The path gate](../95-reference/20-the-path-gate.md) |

## Next

[Grants](./30-grants.md) — per-capability, time-boxed consent.
