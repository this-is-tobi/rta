# Quick start

Ten minutes, three surfaces. Nothing here needs configuration and nothing here writes to your machine.

## 1. Ask it something

```bash
rta sys cpu
```

```
model  Apple M3 Max
cores  14 physical, 14 logical
usage  62.5%
```

Every capability works the same way, and there are 98 of them in the default build:

```bash
rta sys overview            # grouped host health
rta net dns github.com      # DNS, with --type auto
rta cert check example.com  # TLS chain and expiry
rta fs usage ~/Downloads    # what is using space
rta git status              # a repository's state, structured
rta audit web example.com   # TLS, headers, cookies, exposure — graded
```

## 2. Get it in the shape you need

The same command renders five ways. `--output` (or `-o`) is on every command:

```bash
rta sys cpu -o json | jq '.rows'
rta net dns github.com -o csv
rta sys overview -o md >> report.md
```

`pretty` is the default when a human is looking. Scripts should say what they want.

## 3. Open the shell

```bash
rta
```

Bare `rta` on a terminal opens the interactive TUI — a live dashboard with a search bar over every capability, one tile per plugin, and forms for anything that takes input. In a pipe it prints help instead, so a script never hangs on an invisible TUI.

Press `/` to search, `enter` to run, `esc` to go back, `q` to quit.

## 4. Find out what anything does

```bash
rta explain sys.cpu
```

```
id           sys.cpu
summary      Show CPU model, core count and current usage
safety       read
idempotent   true
cli          rta sys cpu [--cores <bool>]
mcp-tool     sys_cpu
input:cores  bool — per-core usage as a bar chart
```

That card is not documentation *about* the capability — it is generated from the same declaration the CLI, the TUI and the MCP schema are built from, so it cannot drift. `rta explain` with no argument lists everything.

## 5. Connect an agent

This is the part worth slowing down for.

```bash
rta mcp install claude
```

That registers rta with Claude Code under the name `claude`. The name matters: grants you issue while talking to one client do not follow every other client on this machine.

For a client that has no command of its own, rta prints exactly what to add and where, and **writes nothing**. It will not edit another tool's config file — that file is what gives an agent access to your secrets, and it is worth reading before it changes.

```bash
rta mcp install cursor --show
```

### What the agent can do now

**Read-only. Everything else is refused.** The agent can call `sys.cpu`, `net.dns`, `git status` and every other read capability. It cannot write, cannot delete, and cannot read your secret store.

That is the default with no configuration, no flags, and no decisions from you.

### Letting it do one more thing

```bash
rta grant allow kv.get db-password --ttl 30m --max-uses 1
```

That allows one key, for thirty minutes, once. Not the store — that key. When any of those three bounds is reached, it stops.

```bash
rta grant list      # what is allowed right now
rta agent log       # what agents actually did, refusals included
```

## 6. Check what you have exposed

```bash
rta doctor
```

Read the `info` rows rather than skipping to the failures. Lines like *"the store unlocks from this environment — an MCP server started here can read secrets, bounded only by grants"* are the ones that tell you what an agent started from this shell inherits.

## Where to go next

| If you want to… | Read |
| --- | --- |
| Script rta, or use it in CI | [The CLI](./10-cli.md) |
| Understand what an agent can reach | [MCP and the safety gate](./20-mcp.md) |
| Grant something narrowly | [Grants](./21-grants.md) |
| Store credentials | [Secrets](./30-secrets.md) |
| Point rta at staging vs production | [Profiles](./40-profiles.md) |
| Add postgres, S3, Vault, Kubernetes | [Using plugins](./50-plugins.md) |
| See it all working together | [Recipes](./90-recipes.md) |
