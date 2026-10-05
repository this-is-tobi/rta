# Harden in five minutes

A checklist for the person who runs rta on a machine an agent will use: six commands, each with the line that tells you it worked. None needs the chapters first, and each step names the page that says why. It bounds the route through rta; an agent that can run shell commands is bounded by what its own permissions allow, which [What rta actually bounds](./10-the-boundary.md) says, and the first step says which of the three situations you are in.

## 1. See where you stand

```bash
rta doctor
rta audit clients
```

`doctor` reports what rta can reach from here, with the rows that need you first: `error`, then `warn`, then `info`, then `ok`, and a last line that counts the notes and names the one that bears most on what an agent can reach. On a machine that has never run rta that is the `grant guard` row, which says issuing a grant is open to anything that runs as you, and the fifth step is its answer. `rta audit clients` says which situation you are in, by looking at what each client config on the machine allows: a `shell` row that fails means the agent can run any command, so rta is the easy route and not the only one. `rta doctor --strict` exits `1` on a `warn` as well as an `error`, so the same check can gate a dotfiles repository or a CI step.

## 2. Connect only what you mean to

```bash
rta init
```

It finds the AI clients on the machine, and for each asks one question with the exact command it would run shown first; Enter skips. A registration grants nothing: the agent starts with the reads that aim nowhere it chooses, and everything else needs a grant you issue. Each client is registered under its own name (`--as claude`), because a grant, a lock and the record all name the agent, so two clients never share a permission. `rta init` registers Claude Code for every project, and `rta mcp install claude` for the directory you are in, which is the narrower choice when you want one. Live consent stays off unless you pass `--consent`, because a parked call in a server nobody watches only makes the agent wait. [Connect an agent](../10-getting-started/30-connect-an-agent.md) has the rest.

## 3. Know your brake before you need it

```bash
rta lock add claude --ttl 1m --note "checking the brake"
rta lock list
rta lock rm claude
```

```
locked         agent claude
effect         refused on its next call — running servers need no restart
shown to them  checking the brake
lifts itself   2026-10-05 23:08
```

A lock refuses every call the agent makes from its next one, reads included, and asks for no passphrase, because an incident is the wrong moment to demand a secret. `rta lock add --all` freezes every agent when you cannot tell which one it is, and `rta lock rm --all` lifts it. Try it once on a minute so the first time is not the bad night. [Stop an agent now](./45-stop-an-agent-now.md) has the whole path.

## 4. Put a ceiling under every grant

```bash
rta policy init
rta policy require
rta policy show
```

`policy init` writes a `.rta-policy.yaml` in this directory with `maxTTL: 1h`: no grant made under it stands longer, whatever it is asked for, and the file can only ever subtract, so it is safe to commit. `policy require` records, outside the repository, that a directory without one is a refusal and not a silence, which is the case a deleted file would otherwise slip through. `policy show` says what is in force and where rta looked. Add what your team never grants (`never: [pg.dump]`), the connections nobody names (`neverProfile: [production]`) and what may only be granted against one record (`requireScope: [kv.get]`). [Team policy](./50-team-policy.md) is the page.

## 5. Keep grants small, and put a passphrase in front of them

```bash
rta grant allow note.add --agent claude --ttl 15m
rta grant list
rta grant revoke --all
rta grant guard on
```

A grant is one capability, optionally one record, for a time that ends on its own: 15 minutes unless you say otherwise and 24 hours at most, and `--max-uses 1` for a value that should be read once. `grant revoke --all` is the end of the task, and prints the line that would issue each grant again. `rta grant guard on` asks for a passphrase, twice, and from then on issuing or renewing a grant asks for it, which is what stops a process running as you from issuing itself one. The `grant guard` row in `rta doctor` turns from `info` to `ok` and names the key. [Grants](./30-grants.md) and [the guard](./35-roles-consent-and-the-guard.md#the-guard-a-passphrase-in-front-of-issuance) say what each costs.

## 6. Read the record

```bash
rta agent overview
rta agent log --refused
rta agent log --detail
```

```
╭──────────┬──────────────┬──────────────────╮
│ AT       │ CAPABILITY   │ RESULT           │
├──────────┼──────────────┼──────────────────┤
│ 23:07:12 │ sys.overview │ refused · locked │
╰──────────┴──────────────┴──────────────────╯
```

`overview` is the last hour: what is waiting on you, who is connected, what needs a grant of yours and what was malformed. `--refused` is the one to reach for first when something is not working, since a refusal is the boundary doing its job, and `--detail` ends with whether the record's hash chain is whole. `rta doctor` surfaces a break without being asked. [The record](./40-audit-trail.md) says how to ship it somewhere durable.

## Related

- [Connecting your AI tool](./60-ai-clients.md) — the detail for each client
- [Roles, consent and the guard](./35-roles-consent-and-the-guard.md) — a day of grants under one word, once grants are routine

## Next

[Team policy](./50-team-policy.md) — a ceiling nobody on the team can raise, which is where this track ends and running it for others begins.
