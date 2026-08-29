# MCP and the safety gate

rta speaks the Model Context Protocol over stdio. An MCP client — Claude Code, VS Code, Cursor, Codex, Gemini, Copilot — launches `rta mcp serve` and gets every capability as a tool, with typed schemas, safety annotations and structured results.

The interesting part is not that it works. It is what an agent can reach before you have decided anything.

## Registering a client

```bash
rta mcp install claude
```

Supported clients: `claude`, `vscode`, `codex`, `gemini`, `cursor`, `copilot`.

Where a client ships its own command for editing its own configuration, rta runs that. Where it does not, rta prints what to add and where, and stops:

```bash
rta mcp install cursor
```

```
Add this to ~/.cursor/mcp.json (or .cursor/mcp.json for one project):

{
  "mcpServers": {
    "rta": {
      "command": "/usr/local/bin/rta",
      "args": ["mcp", "serve", "--as", "cursor"]
    }
  }
}
```

### rta does not write another tool's config file

This is deliberate, and there are three reasons in descending order of importance:

- **That file is what grants an agent access to your secrets.** A tool whose entire argument is that consent should be visible and deliberate has no business writing itself into five agents' permission files unattended.
- **Those files hold things rta must not touch.** VS Code's `mcp.json` is JSONC — comments and all — and often carries API keys in headers. A parse-and-rewrite would destroy comments at best and mishandle a credential at worst.
- **A config format changes when its client changes, not when rta does.** The tool that owns the format is the one that stays correct.

`--show` prints the block without running anything, for any client.

## Naming the agent

```bash
rta mcp install claude --as work-laptop
```

Without a name, every MCP client on your machine is **one principal**: consent given while talking to one follows all the others. `--as` is what keeps an agent's grants its own, and `rta mcp install` always passes it — the default is the client's name.

The name is your word, not the agent's. A client announces itself in the protocol handshake, and rta records that claim, but it does not authorize on it — *a name a thing chooses for itself is not an identity*. What authorizes is the name you typed when you wired the client up.

You will see both in the record: the agent name plainly, the client's self-report in parentheses.

## What is exposed, before you decide anything

**Only read capabilities.** That is the default, and it holds with no flags, no config and no decisions.

| Safety class | Exposed over MCP by default | How to open it |
| --- | --- | --- |
| `read` | ✅ yes | — |
| `write` | ❌ no | `--allow-write <plugin>` |
| `destructive` | ❌ no | `--allow-destructive <capability-id>` |

```bash
rta mcp serve --allow-write todo
rta mcp serve --allow-destructive todo.rm
```

Writes open **one namespace at a time** rather than registry-wide. Destructive capabilities are named individually — there is no wildcard, on purpose.

A destructive capability from an external plugin must be pinned to that binary's digest:

```bash
rta mcp serve --allow-destructive hello.wipe@5dae737f8845
```

So the authorisation attaches to an artifact rather than to a name a replacement would inherit.

### The path gate

Every path argument must sit under a **root**. The default root is the directory the server was started in; widen it with `--root`, which is repeatable.

```bash
rta mcp serve --root ~/projects --root /tmp/scratch
```

The gate governs path *arguments* only. A capability that opens a fixed file of its own — `net hosts list` and `/etc/hosts` — is unaffected, because that path is never an argument for anyone to send.

rta says its roots out loud at startup rather than leaving them to be discovered from a refusal:

```
rta mcp server listening on stdio
path arguments confined to: /Users/you/projects, /tmp/scratch
```

### Two gates, different jobs

The `--allow-*` switches are decided **once**, when the server starts, for every call it will ever make. That is the coarse gate, and it is the right shape for "this agent works on todos".

[Grants](./21-grants.md) are the other half: consent for one capability, optionally one record, expiring on its own. That is the fine gate, and it is what you reach for when the answer is "this once".

## Live consent

Off by default:

```bash
rta mcp serve --consent --consent-notify
```

With `--consent`, a call that needs a grant nobody issued is **parked** instead of refused. You answer it:

```bash
rta agent pending
rta agent show 3        # everything about it, including what it would do
rta agent allow 3
rta agent deny 3
```

`--consent-preview` (on by default) runs the capability's own `--dry-run` and shows the result on the parked request, which changes the question from *"may this agent call `todo.rm`"* to *"may it remove **this task**"*.

`--consent-wait` bounds how long a call waits before it is refused anyway (default 90s).

**The default is off on purpose.** A call parked in a server nobody is watching is worse than a refusal: the agent hangs, you never see it, and the timeout is the only thing that resolves it. Turn consent on when you are actually at the machine.

## Environment inheritance

An MCP server inherits the environment it was started from. If your secret store unlocks without a passphrase in that environment, the server can open it — bounded by grants, but able to.

```bash
rta doctor
```

```
kv store    info    unlocks from this environment — an MCP server started here
                    can read secrets, bounded only by grants
```

That line is the whole warning. It is not a misconfiguration; it is a fact about how you set the store up, and it is worth knowing before you connect a client.

## Next

- [Grants](./21-grants.md) — per-capability, time-boxed consent
- [The record](./22-audit-trail.md) — what actually happened
- [Team policy](./23-team-policy.md) — a ceiling nobody can raise
