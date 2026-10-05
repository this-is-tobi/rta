# Grants

A grant is permission for **one capability**, optionally **one record**, that **expires on its own**.

It is the whole of the model. A read that stays on this machine is free; **everything that changes anything costs a grant**, and so does a read aimed at a destination the agent names — a host, a URL, a port. There is no flag that stands in for one. A grant is decided when you need it, for as little as you need, and then stops being true without anybody remembering to revoke it.

## Issuing one

```bash
rta grant allow kv.get db-password --agent claude --ttl 30m
```

That reads as: allow the agent `claude` to call `kv.get`, but only on the key `db-password`, for thirty minutes.

| Part | What it means |
| --- | --- |
| `<target>` | A capability ID (`kv.get`) or a plugin name (`kv`, covering all of it, destructive capabilities included) |
| `[scope]` | One record — a key, a table, a bucket — or, ending in `/`, a folder of them. Omit to cover the whole capability |
| `--ttl` | How long: `30s`, `15m`, `2h`. **Default 15m, maximum 24h** |
| `--agent` | Narrow to one named agent — the name `rta mcp serve --as` uses |
| `--profile` | Narrow to one configured connection (staging, not production) |
| `--max-uses` | Expire after this many successful calls |
| `--rate` | Bound how *fast*, as calls/window — `10/1h` |
| `--note` | Why, shown by `grant list` |

**Only a person at a terminal can issue one.** An agent that could grant itself access would be no gate at all, so there is no [MCP](../95-reference/10-glossary.md#acronyms) tool for this and no flag that makes one.

## The four bounds, and what each is for

They compose. A grant stops at whichever is reached first.

```bash
rta grant allow kv.get deploy-key --agent claude --ttl 5m --max-uses 1
```

- **`--ttl` bounds time.** The one you always get, because it is the only bound that keeps working when you forget.
- **`scope` bounds reach.** `rta grant allow kv.get` allows the whole store. `rta grant allow kv.get db-password` allows one key. Reach for the second unless you mean the first. A record ending in `/` is a folder: `rta grant allow kv.get prod/` allows every key under `prod/`, those written later included, and never one that climbs back out — `prod/../staging/db-password`, or any spelling a server decodes into that, `%2e%2e` and `..%2f` among them. Name such a record whole if you mean it; a folder never sweeps one in. A record is compared as the call spells it, white space included: a grant on `db-password` does not cover ` db-password` or `db-password` followed by a no-break space, which a store reads as another key and a web server as another path, so a call naming one asks for its own consent. An audit whose record is a host, a domain or a namespace refuses one with white space around it, since no such name has any, rather than audit the name without it: `audit web`, `audit mail` and the `audit kube` checks alike. `audit web` refuses a host holding a character that draws as nothing as well, wherever it stands, since the request would drop it before the name resolves and audit the name without it all the same, and a host in anything but ASCII, typed or percent-encoded, which the request folds into another name the same way: a fullwidth digit into its digit, an ideographic full stop into a dot. An internationalised name goes in its `xn--` form, the name the request resolves. `grant allow`, `grant renew` and `grant revoke` take a record exactly as you give it, so the command a refusal offers for such a call, quoted for your shell, grants that call's record and not the one it looks like; a record that is only white space names nothing and is refused, by every way a grant is issued, and a refusal an agent gets for a call naming one offers no command for it. It is shown the way it is compared: a record with white space in it, or with a character that draws as nothing, is shown quoted as Go quotes a string, each such character written as the escape of its code point, wherever you answer for one: `agent pending`, `agent show`, the answer and `grant list` alike. A record shown as it is never begins with a quotation mark, so the two are never shown alike. `agent log` shows the records each call named the same way, in a record column, and the agent record keeps them exactly as the call spelled them. The arguments shown beside a record, in `agent log`, in the would-do column of `agent pending` and on `agent show`, are kept as a model may read them, without the zero-width, direction and tag characters, so an argument can read as the bare record where the record beside it is shown padded: the record is the one to answer for.
- **`--max-uses` bounds quantity.** `--max-uses 1` is the shape for a value that should be read exactly once — a deploy key, a one-time token.
- **`--rate` bounds speed.** `--rate 10/1h` allows ten calls in any hour and tells the agent when to come back. A session that has gone wrong slows to something you can notice, rather than draining at machine speed.

The last one is worth dwelling on. Time and quantity both fail the same way: an agent in a loop exhausts them immediately and correctly. A rate limit is the only one of the four that turns a runaway into something a human can catch while it is happening.

## Naming the agent

```bash
rta grant allow pg.query --agent claude --profile staging --ttl 1h
```

A grant is for one agent, and there is no grant for every client: consent given while pairing with one editor should not silently follow the agent running in a CI container. Leave `--agent` out and rta fills the name in when this machine knows exactly one agent — one that has connected, or that already holds a grant — asks which when it knows several (`grant.whichagent`), and refuses when it knows none (`grant.noagent`), since a grant for nobody would be a row `grant list` shows and the gate ignores.

The name comes from `rta mcp serve --as <name>`, which `rta mcp install` sets for you. Both halves name the same thing on purpose.

An empty agent on a grant is not a wildcard — it matches a server started without `--as`, and nothing else. Matching is exact in both directions, which is the same rule `--profile` already used.

## Seeing and taking back

```bash
rta grant list                       # what is allowed right now
rta grant renew kv.get db-password   # push out the deadline
rta grant revoke kv.get db-password  # take it back now
rta grant revoke kv                  # or all of it
rta grant revoke kv.get --agent claude --exact   # the one grant naming no record, on the base connection
```

Each selector narrows, and one left out matches every grant: `grant revoke kv.get` takes back the grant on every record, every connection and every agent, and a plugin name everything inside it. `--exact` makes the selectors name one grant — a record, `--profile` or `--agent` left out means the grant naming none, and a plugin name its grant on the whole plugin alone — which is the only way to name the grant on no record, or on the base connection, without taking its neighbours with it. `grant renew` takes it too. In the TUI, `x` and `n` on a row of `grant list` act on that row's grant alone, as `--exact`, and `x` on a [roster](../95-reference/10-glossary.md#terms) read with `--server` does so on that server: one older than the switch would read it as absent, so it is asked first and refused rather than trusted. `n` is not offered on such a roster, since `grant renew` takes no `--server`: a renewal of that server's grants is made there.

`grant list` shows the target, the scope, what remains of each bound, the agent and profile it is narrowed to, and your `--note`. `--detail` adds the plugin build each grant is bound to: the short digest of the plugin's artifact, or `built in`. A grant whose plugin has been replaced since it was issued is marked `(replaced)` on every listing of this machine's grants, and one whose plugin rta no longer loads `(not loaded)`, each with a warning beside the rows saying what fixes it. A roster read with `--server` is judged by that server, against the plugins that answer there, which this machine cannot see, and carries the same marks and warnings; a server on an older rta sends no verdict, and each of its grants is marked `(unknown)`. It is the answer to "what can an agent do right now", and it is the one screen worth checking before you walk away from a machine with a server running.

Revoking takes back what grants gave — it does not touch the ungated read tools an agent's token still opens. When the need is "this agent makes no call of any kind until I say so", that is a [lock](./45-stop-an-agent-now.md): `rta lock add <name>`, effective on its next call, no restart.

## Bound to a task, not to a process

The server is per-session by construction, so the question of how long an agent's reach lasts is answered by the permissions, and those have their own clocks rather than the process's:

| Bound to a task by | What it does |
| --- | --- |
| `rta grant allow … --ttl 30m` | Consent that expires on its own, whatever the server does |
| `rta grant allow … --max-uses 5` | Consent that runs out by use rather than by clock |
| `rta use staging` | While it is on, every *other* environment is refused whatever grants exist, as [Profiles](../20-using/40-profiles.md#switching-authorizes-nothing) explains |
| `rta grant revoke --all` | The end of the task, without touching the client |

Restarting the server changes none of it. That is deliberate: a deadline that ended when a process did would be a deadline your editor could reset by crashing.

## What a grant does not do

- **It does not survive the plugin it names being replaced.** A grant on a plugin's capability records that plugin's artifact digest, so swapping the binary under the same name stops it covering anything. Built-ins have no separate artifact and carry no digest. Upgrading or rebuilding a plugin is such a replacement: `grant list` marks every grant standing on the old build `(replaced)` and `rta doctor` warns of them, because an agent refused under one is told only what an ungranted call is told. Issue it again after the upgrade with `rta grant allow`; `grant renew` moves the deadline and never rebinds a grant.
- **It does not widen a path root.** Path confinement is checked separately, on every path argument.
- **It does not survive a ceiling.** If a `.rta-policy.yaml` says `maxTTL: 15m`, a `--ttl 2h` grant is clamped to 15m and told so.
- **It does not authorize a profile you have not configured.** `--profile staging` matches the connection named `staging`, exactly.
- **It does not blur instances.** When an environment holds several connections to one plugin — `pg` and `pg/analytics` — a grant names exactly one (`--profile staging/analytics`), and asking for the bare name is refused with the list rather than resolved into a consent you did not give. Each grant pins the instance it was issued against, so re-aiming that one connection revokes exactly that grant.

## Related

- [Roles, consent and the guard](./35-roles-consent-and-the-guard.md) — a day of grants under one word, answering a parked call, the file's seal, and a passphrase in front of issuance
- [Team policy](./50-team-policy.md) — a ceiling nobody on the team can raise
- [Profiles](../20-using/40-profiles.md) — what `--profile` is naming

## Next

[The record](./40-audit-trail.md) — what agents actually did with what you granted.
