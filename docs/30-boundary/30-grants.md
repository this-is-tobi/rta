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

**Only a person at a terminal can issue one.** An agent that could grant itself access would be no gate at all, so there is no MCP tool for this and no flag that makes one.

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

Each selector narrows, and one left out matches every grant: `grant revoke kv.get` takes back the grant on every record, every connection and every agent, and a plugin name everything inside it. `--exact` makes the selectors name one grant — a record, `--profile` or `--agent` left out means the grant naming none, and a plugin name its grant on the whole plugin alone — which is the only way to name the grant on no record, or on the base connection, without taking its neighbours with it. `grant renew` takes it too. In the TUI, `x` and `n` on a row of `grant list` act on that row's grant alone, as `--exact`, and `x` on a roster read with `--server` does so on that server: one older than the switch would read it as absent, so it is asked first and refused rather than trusted. `n` is not offered on such a roster, since `grant renew` takes no `--server`: a renewal of that server's grants is made there.

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

## Roles: a day of grants under one word

A grant is one target. A day of work is a dozen, and with the guard on that was a dozen passphrases. A **role** is the list, written once, and `grant issue` is that list issued to one agent under one prompt:

```yaml
# ~/.config/rta/config.yaml — yours
roles:
  dev:
    ttl: 8h
    grants:
      - kv.get db-password
      - pg.query --profile staging --rate 100/1h
      - note
```

```bash
rta grant roles                      # what this machine can issue, every line of each, and the window each will really get
rta grant issue dev --agent claude   # every line, one passphrase
rta grant list --role dev            # what stands under it
rta grant renew --role dev           # move the whole bundle's deadline, one passphrase
rta grant revoke --role dev          # take the whole bundle back
```

Each line is in the grammar `rta grant allow` takes — a target, an optional record, and `--profile`, `--ttl`, `--max-uses`, `--rate` or `--note` — and is built and checked exactly as a typed grant is: the target has to exist and need a grant, the [ceiling](./50-team-policy.md) caps and forbids each line, the guard signs each grant, a [lock](./45-stop-an-agent-now.md) still overrides all of them. The grants last `--ttl`, else the role's `ttl:`, else twelve hours; a line with its own `--ttl` keeps the shorter. `grant list` shows the role on each row.

**The bundle has no id of its own.** It is "role dev, issued to claude", and that pair already names it: the grant file keeps one row per target, record, connection and agent, so issuing a role whose lines already stand refreshes them rather than doubling them. A line that replaces a grant you issued by hand — with an hour left, say, under the role's twelve — is a widening you did not type, so the receipt says what each line replaced, before the passphrase asks for anything. `revoke --role` says what still stands afterwards, computed rather than promised.

**A role is not something an agent holds.** The agent still presents its `--as` name and the gate still reads grants; the role name on a grant is your bookkeeping, signed with the rest so a grant cannot be re-filed under a role it was not issued under. Revoking a role is not a lock: the agent may be granted things again a minute later.

**A role from the team's file is somebody else's list.** `.rta-policy.yaml` may carry `roles:` beside the ceiling that bounds them, so a team shares the bundle and the limit in one committed file. That file grants nothing by existing — a person issues the role, at a terminal — but an edit could widen a role a person then issues with one word. So a team's role is issued at the command line only, where its lines are printed before the passphrase asks for them, and with the guard off it wants `--yes` after `rta grant roles <name>` has been read. Your own roles, in your config or your policy file or a file you named with `RTA_POLICY`, ask nothing extra: you wrote them. The same name in two files is refused rather than resolved by precedence, and `rta doctor` says so ahead of time.

## Live consent, when you would rather be asked

Off by default, and an option of the server, so it goes into the registration:

```bash
rta mcp install claude --consent --consent-notify
```

(`rta mcp serve --consent --consent-notify` is the same two flags for a server you start yourself.) With `--consent`, a call that needs a grant nobody issued is **parked** instead of refused. You answer it:

```bash
rta agent pending
rta agent show 5473aa62        # everything about it, including what it would do
rta agent allow 5473aa62
rta agent deny 5473aa62
```

A destructive call is previewed before it parks: rta runs the capability's own `--dry-run` and shows the result on the request, which changes the question from *"may this agent call `note.rm`"* to *"may it remove **this note**"*. The preview is not optional, and it is bounded to built-in capabilities, whose dry runs are cheap and honest about `DryRun` by test — a plugin's handler is never run to answer a question about it.

Answering `allow` runs that one call. It does not create a standing grant — if the agent asks again, you are asked again. That is the difference between consent and permission, and rta keeps them separate.

`rta agent allow <id> --ttl 1h` also issues the grant the call was missing, for the record it named and no wider — a grant for each record when it names several, as a `kv.rename` names the key and where it goes. A call naming a record ending in `/` — a folder to a grant, `https://` included, and the agent chose it — is released on its own and no grant is issued; the answer names the `rta grant allow` calls that issue them on purpose, if every record under the folder is what you mean. A call naming a record that is only white space is released on its own too, since no grant can name that record: `grant allow` refuses it, and `grant revoke` and `grant renew` could never take one back by it. The team's ceiling holds the answer record by record, as it would hold each grant.

`--consent-wait` bounds how long a call waits before it is refused anyway (default 90s).

**The default is off on purpose.** A call parked in a server nobody is watching is worse than a refusal: the agent hangs, you never see it, and the timeout is the only thing that resolves it. Turn consent on when you are actually at the machine — or, for a remote server, when [the operator channel](./66-operators.md) gives its enrolled operators a way to answer with `--server`.

The same three commands take `--server <name>` to answer a call parked on a remote server, as a signed call over [the operator channel](./66-operators.md) — where, unlike here, even the one-shot answer costs your operator key's passphrase, because "an agent with a shell could have done this anyway" is true at your terminal and false across a network.

## The file, and why it is sealed

Grants live in `~/.local/share/rta/grants.json`, and the file carries a tamper seal.

The seal stops a forged line from a process that cannot read the key. It does not stop an agent that can run `rta grant allow` itself — which is why every grant also records [whether anybody was at the terminal when it was issued](./10-the-boundary.md#what-a-grant-says-about-where-it-came-from).

The reason is asymmetry: **a forged line in a grant file *adds* permission.** Anything that can write that file can write itself an allowance, and rta would honour it. So the file is sealed, and a grant file that does not verify is not honoured — `rta doctor` says so plainly rather than failing quietly.

This is exactly why a [team policy ceiling](./50-team-policy.md) needs no seal: it can only ever subtract, so the worst a hostile edit achieves is making rta refuse more.

## The guard: a passphrase in front of issuance

The seal's limit above has an answer, and it is opt-in:

```bash
rta grant guard on
```

From then on, issuing or renewing a grant asks for a passphrase — including `rta agent allow --ttl`, which also mints one. Every grant is signed with a key that exists only encrypted under that passphrase, and a grant without a valid signature is not honoured. The difference from the seal is one of kind, not degree: the seal's key sits on disk where anything running as you can read it, while the guard's passphrase lives in your head. An agent that runs `rta grant allow` from its shell is *refused*, however it invokes the binary — the ordinary self-granting path moves from detection (the Origin column, after the fact) to prevention.

Honest edges, stated rather than implied:

- **Enabling and disabling both clear the grant file.** Grants issued without a passphrase would be laundered by blessing them wholesale, and signatures with no guard beside them read as tampering. Grants last a day at most; re-issuing costs minutes.
- **Revoking never asks.** Taking authority away is the fail-safe direction, and an incident is the wrong moment to demand a secret.
- **A forgotten passphrase costs at most a day.** `rm` the guard state, `rta grant revoke --all`, `rta grant guard on` with a new passphrase — loud, bounded, and no secret is ever recoverable from disk.
- **File tampering stays in the detection regime.** Something running as you can still delete the guard's state or swap its key; every such rewrite rta can notice is refused loudly and fails closed. The cheapest rollback is worth naming: deleting the guard state *and* the grant file together leaves a machine indistinguishable from one where the guard was never enabled. A running MCP server pins the guard state at startup and refuses every grant-gated call if it weakens mid-session — which covers exactly the session the attacker is talking through. Across restarts nothing on disk can testify, and [the boundary chapter](./10-the-boundary.md) owns what remains.
- **One-shot consent answers stay passphrase-free.** `rta agent allow <id>` without `--ttl` releases exactly one already-parked call — something an agent with a shell could have run directly — and every such call is in the record. The guard prices authority that *outlives* the conversation; the harness deny list from `rta audit clients --fix` is the layer that stops an agent answering its own questions at all.
- **The passphrase never travels on the command line.** `--passphrase` is refused from the CLI — argv is readable by every process you run and lands in shell history — so the channels are the prompt and the TUI's masked field, both of which land nowhere.
- **There is no environment variable for the passphrase, and there will not be one.** The kv store accepts `RTA_KV_PASSPHRASE` because some setups need unattended unlocks, and `rta doctor` warns about the inheritance. The guard exists for the opposite trade: issuance is rare, attended, and nothing an agent inherits may satisfy it.

`rta grant guard status` says whether it is on, since when, and under which key; `rta doctor` carries the same fact.

### Remote mode: a guard whose keys are elsewhere

For a machine whose humans are not at its terminal — an `rta mcp serve --http` gateway — the guard has a second shape: `rta grant guard remote operators.txt --url https://rta.example.com` enrolls the public keys from an operator roster (minus any `role=read` rows — a watching key is not a signing key), bound to this server's canonical URL, and from then on a grant is honoured only when one of those keys signed it for this server — the URL sits inside the signed authority, so a fleet sharing one roster stays many trust domains: a row signed for staging is refused on prod however its bytes travel. No key material lives on the machine at all: nothing to steal, no passphrase to phish out of a server process, and `rta grant allow` at its own shell finds nothing to unlock — refused by construction, which on a remote server is the boundary completing itself rather than a gap. Issuance happens from an enrolled operator's own machine over [the operator channel](./66-operators.md), each grant signed there under that operator's passphrase and attributed to their roster label in the listing's Origin column. Turning remote mode off asks for no passphrase — there is none here to ask for — so it costs presence at the machine's terminal, and clears the grants the operators signed, the same clean-slate rule as every other guard transition.

## What a grant does not do

- **It does not survive the plugin it names being replaced.** A grant on a plugin's capability records that plugin's artifact digest, so swapping the binary under the same name stops it covering anything. Built-ins have no separate artifact and carry no digest. Upgrading or rebuilding a plugin is such a replacement: `grant list` marks every grant standing on the old build `(replaced)` and `rta doctor` warns of them, because an agent refused under one is told only what an ungranted call is told. Issue it again after the upgrade with `rta grant allow`; `grant renew` moves the deadline and never rebinds a grant.
- **It does not widen a path root.** Path confinement is checked separately, on every path argument.
- **It does not survive a ceiling.** If a `.rta-policy.yaml` says `maxTTL: 15m`, a `--ttl 2h` grant is clamped to 15m and told so.
- **It does not authorize a profile you have not configured.** `--profile staging` matches the connection named `staging`, exactly.
- **It does not blur instances.** When an environment holds several connections to one plugin — `pg` and `pg/analytics` — a grant names exactly one (`--profile staging/analytics`), and asking for the bare name is refused with the list rather than resolved into a consent you did not give. Each grant pins the instance it was issued against, so re-aiming that one connection revokes exactly that grant.

## Related

- [Team policy](./50-team-policy.md) — a ceiling nobody on the team can raise
- [Profiles](../20-using/40-profiles.md) — what `--profile` is naming

## Next

[The record](./40-audit-trail.md) — what agents actually did with what you granted.
