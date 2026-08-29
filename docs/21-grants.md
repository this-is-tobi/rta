# Grants

A grant is permission for **one capability**, optionally **one record**, that **expires on its own**.

It is the fine-grained half of the model. The `--allow-write` and `--allow-destructive` switches on `rta mcp serve` are decided once, at startup, for every call the server will ever make. A grant is decided when you need it, for as little as you need, and then stops being true without anybody remembering to revoke it.

## Issuing one

```bash
rta grant allow kv.get db-password --ttl 30m
```

That reads as: allow `kv.get`, but only the key `db-password`, for thirty minutes.

| | |
| --- | --- |
| `<target>` | A capability ID (`kv.get`) or a plugin name (`kv`, covering all of it) |
| `[scope]` | One record — a key, a table, a bucket. Omit to cover the whole capability |
| `--ttl` | How long: `30s`, `15m`, `2h`. **Default 15m, maximum 24h** |
| `--agent` | Narrow to one named agent — the name `rta mcp serve --as` uses |
| `--profile` | Narrow to one configured connection (staging, not production) |
| `--max-uses` | Expire after this many successful calls |
| `--rate` | Bound how *fast*, as calls/window — `10/1h` |
| `--note` | Why, shown by `grant list` |

**Only a person at a terminal can issue one.** An agent that could grant itself access would be no gate at all, so there is no MCP tool for this and no flag that makes one.

## The four bounds, and what each is for

They compose. A grant stops at whichever is reached first.

```bash
rta grant allow kv.get deploy-key --ttl 5m --max-uses 1
```

- **`--ttl` bounds time.** The one you always get, because it is the only bound that keeps working when you forget.
- **`scope` bounds reach.** `rta grant allow kv.get` allows the whole store. `rta grant allow kv.get db-password` allows one key. Reach for the second unless you mean the first.
- **`--max-uses` bounds quantity.** `--max-uses 1` is the shape for a value that should be read exactly once — a deploy key, a one-time token.
- **`--rate` bounds speed.** `--rate 10/1h` allows ten calls in any hour and tells the agent when to come back. A session that has gone wrong slows to something you can notice, rather than draining at machine speed.

The last one is worth dwelling on. Time and quantity both fail the same way: an agent in a loop exhausts them immediately and correctly. A rate limit is the only one of the four that turns a runaway into something a human can catch while it is happening.

## Naming the agent

```bash
rta grant allow pg.query --agent claude --profile staging --ttl 1h
```

Without `--agent`, a grant covers **every** MCP client on this machine. That is rarely what you mean once you have more than one: consent given while pairing with one editor should not silently follow the agent running in a CI container.

The name comes from `rta mcp serve --as <name>`, which `rta mcp install` sets for you. Both halves name the same thing on purpose.

An empty agent on a grant is not a wildcard — it matches a server started without `--as`, and nothing else. Matching is exact in both directions, which is the same rule `--profile` already used.

## Seeing and taking back

```bash
rta grant list                       # what is allowed right now
rta grant renew kv.get db-password   # push out the deadline
rta grant revoke kv.get db-password  # take it back now
rta grant revoke kv                  # or all of it
```

`grant list` shows the target, the scope, what remains of each bound, the agent and profile it is narrowed to, and your `--note`. It is the answer to "what can an agent do right now", and it is the one screen worth checking before you walk away from a machine with a server running.

## Live consent, when you would rather be asked

With `rta mcp serve --consent`, a call that needs a grant nobody issued is **parked** rather than refused:

```bash
rta agent pending
rta agent show 3      # what it would do, from the capability's own --dry-run
rta agent allow 3
```

Answering `allow` runs that one call. It does not create a standing grant — if the agent asks again, you are asked again. That is the difference between consent and permission, and rta keeps them separate.

See [MCP and the safety gate](./20-mcp.md#live-consent) for why this is off by default.

## The file, and why it is sealed

Grants live in `~/.local/share/rta/grants.json`, and the file carries a tamper seal.

The reason is asymmetry: **a forged line in a grant file *adds* permission.** Anything that can write that file can write itself an allowance, and rta would honour it. So the file is sealed, and a grant file that does not verify is not honoured — `rta doctor` says so plainly rather than failing quietly.

This is exactly why a [team policy ceiling](./23-team-policy.md) needs no seal: it can only ever subtract, so the worst a hostile edit achieves is making rta refuse more.

## What a grant does not do

- **It does not open a safety class.** If `todo.rm` is destructive and the server was started without `--allow-destructive todo.rm`, no grant makes it reachable. The startup gate is upstream of grants and is not negotiable at run time.
- **It does not widen a path root.** Path confinement is checked separately, on every path argument.
- **It does not survive a ceiling.** If a `.rta-policy.yaml` says `maxTTL: 15m`, a `--ttl 2h` grant is clamped to 15m and told so.
- **It does not authorize a profile you have not configured.** `--profile staging` matches the connection named `staging`, exactly.

## Next

- [The record](./22-audit-trail.md) — what agents actually did with what you granted
- [Team policy](./23-team-policy.md) — a ceiling nobody on the team can raise
- [Profiles](./40-profiles.md) — what `--profile` is naming
