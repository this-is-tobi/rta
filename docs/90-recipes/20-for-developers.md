# For a developer

You want your agent useful on what you are working on and nowhere near what you are not. The day comes down to a few things: environments named once, secrets that never reach a command line, an agent that reads freely and asks before it changes anything, and a record you can check afterwards. Each section is one task and the chapter behind it.

## Set the machine up, once

```bash
rta kv init --generate     # a dedicated key for your secret store
rta mcp install claude     # register rta with your agent, under the name claude
rta doctor
```

With `--generate` nothing asks for a passphrase again, and losing that key costs this store and nothing else. `mcp install` registers `rta mcp serve --as claude`, and the name is what grants follow, so another client registered under another name gets none of them. Read doctor's `info` rows: *the store unlocks from this environment* is a fact about what your agent's server inherits. [Secrets](../20-using/50-secrets.md) · [Connecting your AI tool](../30-boundary/60-ai-clients.md).

## Name each environment once

```bash
rta kv set staging-db-password   # asks for it, echoing nothing, so no shell history holds it
rta profile set staging --note "shared staging" --ttl 8h \
    --plugin pg --set host=db.staging.internal --set database=app \
    --secret password=kv:staging-db-password
rta profile list
```

A value typed after the key would land in your shell history, so `kv set` asks for it here, at the terminal and without echoing it; one already in a file comes from `--file`, and the TUI's `kv set` form masks it. A profile points every plugin that has something in it at one environment, and its `secrets:` hold a reference, never a value. In the TUI it is one form: `f`, then `n`. [Profiles](../20-using/40-profiles.md).

## Work in one environment at a time

```bash
rta use staging --for 2h
rta pg query "select count(*) from orders"   # no --profile: staging is on
rta use --off
```

While a profile is on, `rta mcp serve` refuses every other one, whatever grants exist. It only takes away, so it is the safe thing to reach for first. [Switching authorizes nothing](../20-using/40-profiles.md#switching-authorizes-nothing).

## Give your agent one thing, for a while

```bash
rta grant allow pg.query --profile staging --agent claude --ttl 1h --rate 30/1h
rta grant allow kv.get staging-db-password --agent claude --ttl 5m --max-uses 1
rta grant list
```

A read costs nothing until it names a profile. Anything that changes something, or returns what somebody stored, costs a grant: one capability, optionally one record, gone on its own. `--rate` is the bound people skip and the one that makes a runaway visible while it is happening. When your team keeps roles, `rta grant roles` lists them and `rta grant issue dev --agent claude` issues a day's worth at once. [Grants](../30-boundary/30-grants.md) · [Pair with an agent on a staging database](./01-readme.md#pair-with-an-agent-on-a-staging-database-for-an-hour).

## Answer it while you work

```bash
rta agent pending
rta agent show 5473aa62    # what the call would do, from its own dry run
rta agent allow 5473aa62
```

The agent tile on the TUI's dashboard counts the calls waiting for you: `w` opens the queue, `enter` shows what a call would do, `a` allows it and `d` denies it. Allowing runs that one call and leaves no standing grant behind. A call parks instead of being refused only on a server started with `--consent` — [be asked instead of refused](./01-readme.md#be-asked-instead-of-refused-while-you-are-at-the-machine) says when that is worth it and when it is not.

## See what it did

```bash
rta agent log --limit 20
rta agent log --refused
```

Every call that came over MCP is a line — refusals included, secret arguments masked — and `g` in the TUI opens the same record. What you run at your own terminal is not in it: the record is what the agent did. [The record](../30-boundary/40-audit-trail.md).

## Secrets, day to day

```bash
eval "$(rta kv env staging-db-password)"    # STAGING_DB_PASSWORD, in this shell only
rta kv get tls-cert --out /tmp/server.pem   # written at 0600
rta kv history staging-db-password          # when each earlier value was set, never the value
```

A value pasted over the wrong key is undone by name, and `rta kv status` answers without unlocking anything. [Undoing a mistake](../20-using/50-secrets.md#undoing-a-mistake).

## Keep the TUI open

```bash
rta
rta dashboard add pg.overview --profile staging
```

`/` searches every capability, `f` is your environments, and `+` puts a capability on the dashboard. The tiles rta picks on its own never reach off the machine; one that does is yours to add, pinned to the connection you name. [The TUI](../20-using/20-tui.md).

## End of the task

```bash
rta grant revoke --all
rta use --off
```

Grants lapse on their own. Revoking is for a task that ended early, or for walking away from a machine with a server still running.

## Next

- [For a security team](./10-for-security-teams.md) — the same boundary, from the side that owns it
- [An agent in a cluster](./30-an-agent-in-a-cluster.md) — when the agent should run somewhere else and hold nothing
- [Profiles](../20-using/40-profiles.md) · [Grants](../30-boundary/30-grants.md) · [The TUI](../20-using/20-tui.md)
