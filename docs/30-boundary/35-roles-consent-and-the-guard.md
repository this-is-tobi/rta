# Roles, consent and the guard

[Grants](./30-grants.md) is the everyday page: issuing one, the four bounds, seeing and taking them back. This page is what a team or a careful person adds on top: a role that issues a day of grants under one word, live consent for when you would rather be asked than refuse, the file the grants live in, and a passphrase in front of issuing them.

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
- **File tampering stays in the detection regime.** Something running as you can still delete the guard's state or swap its key; every such rewrite rta can notice is refused loudly and fails closed. The cheapest rollback is worth naming: deleting the guard state *and* the grant file together leaves a machine indistinguishable from one where the guard was never enabled. A running [MCP](../95-reference/10-glossary.md#acronyms) server pins the guard state at startup and refuses every grant-gated call if it weakens mid-session — which covers exactly the session the attacker is talking through. Across restarts nothing on disk can testify, and [the boundary chapter](./10-the-boundary.md) owns what remains.
- **One-shot consent answers stay passphrase-free.** `rta agent allow <id>` without `--ttl` releases exactly one already-parked call — something an agent with a shell could have run directly — and every such call is in the record. The guard prices authority that *outlives* the conversation; the harness deny list from `rta audit clients --fix` is the layer that stops an agent answering its own questions at all.
- **The passphrase never travels on the command line.** `--passphrase` is refused from the CLI — argv is readable by every process you run and lands in shell history — so the channels are the prompt and the TUI's masked field, both of which land nowhere.
- **There is no environment variable for the passphrase, and there will not be one.** The kv store accepts `RTA_KV_PASSPHRASE` because some setups need unattended unlocks, and `rta doctor` warns about the inheritance. The guard exists for the opposite trade: issuance is rare, attended, and nothing an agent inherits may satisfy it.

`rta grant guard status` says whether it is on, since when, and under which key; `rta doctor` carries the same fact.

### Remote mode: a guard whose keys are elsewhere

For a machine whose humans are not at its terminal — an `rta mcp serve --http` gateway — the guard has a second shape: `rta grant guard remote operators.txt --url https://rta.example.com` enrolls the public keys from an operator [roster](../95-reference/10-glossary.md#terms) (minus any `role=read` rows — a watching key is not a signing key), bound to this server's canonical URL, and from then on a grant is honoured only when one of those keys signed it for this server — the URL sits inside the signed authority, so a fleet sharing one roster stays many trust domains: a row signed for staging is refused on prod however its bytes travel. No key material lives on the machine at all: nothing to steal, no passphrase to phish out of a server process, and `rta grant allow` at its own shell finds nothing to unlock — refused by construction, which on a remote server is the boundary completing itself rather than a gap. Issuance happens from an enrolled operator's own machine over [the operator channel](./66-operators.md), each grant signed there under that operator's passphrase and attributed to their roster label in the listing's Origin column. Turning remote mode off asks for no passphrase — there is none here to ask for — so it costs presence at the machine's terminal, and clears the grants the operators signed, the same clean-slate rule as every other guard transition.

## Related

- [Grants](./30-grants.md) — the everyday page
- [Connect an agent](../10-getting-started/30-connect-an-agent.md) — consent needs a server you started with `--consent`

## Next

[The record](./40-audit-trail.md) — what agents actually did with what you granted.
