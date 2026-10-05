# Connecting your AI tool

rta is an MCP server over stdio, so anything that speaks MCP can use it. This chapter is the per-client detail: where each one keeps its configuration, what `rta mcp install` will and will not do for it, and how to check afterwards that it actually worked.

Every client below is registered under a name: grants are issued to it and a lock freezes it, so consent you give while talking to one client does not follow the others. `rta mcp install` always passes it, and the default is the client's own name. [Connect an agent](../10-getting-started/30-connect-an-agent.md#name-it) says why that name is the whole point, and how to check afterwards that the connection works.

## The short version

```bash
rta mcp install claude
```

That is it for a client that ships its own configuration command. For one that does not, the same command prints exactly what to add and where, and writes nothing. Any other client that speaks MCP over stdio gets the standard block too — `rta mcp install windsurf` — with a line saying rta does not know where that client keeps its configuration.

The answer says where the client's command registered rta (`scope`), what to do next and what the agent can reach. [Server options](../10-getting-started/30-connect-an-agent.md#server-options-belong-in-the-registration) — `--consent`, `--root`, `--max-result` — go into the same command, and [running it again](../10-getting-started/30-connect-an-agent.md#running-it-again) replaces a registration that differs rather than keeping it.

On a new machine, `rta init` does this for every client it finds: one question each, showing the command it would run, Enter to skip, and `--yes` to say yes to all of them without asking. It registers Claude Code for every project, leaves a client that already has a registration as it is, and writes no config file of its own — see [Configuration](../95-reference/50-where-rta-keeps-things.md#configuration).

## What rta does for each client

| Client | `rta mcp install` | Configuration file | Verified |
| --- | --- | --- | --- |
| Claude Code | runs `claude mcp add` | `.mcp.json`, or `~/.claude.json` | ✅ against the real CLI |
| VS Code | runs `code --add-mcp` | VS Code's user `mcp.json` | ✅ against the real CLI |
| Cursor | prints the block | `~/.cursor/mcp.json`, or `.cursor/mcp.json` per project | block only — Cursor has no CLI for this |
| GitHub Copilot CLI | prints the block | `~/.copilot/mcp-config.json` | block only — Copilot configures MCP from its own `/mcp` prompt |
| OpenAI Codex CLI | runs `codex mcp add` | `~/.codex/config.toml` | ⚠ command declared, not verified |
| Gemini CLI | runs `gemini mcp add` | `~/.gemini/settings.json` | ⚠ command declared, not verified |

The "verified" column is honest rather than reassuring. Two of these commands were written from their documented interface and have not been run against the tool itself, so if one has moved, rta falls back to printing the block instead of failing — which is the whole reason the fallback exists.

`--show` prints the block without running anything, for any client:

```bash
rta mcp install codex --show
```

`--global` installs for every project instead of just the one rta happens to run in, where the client actually supports the distinction — Claude Code does, and rta passes its own `--scope user` through. Where a client's own command is declared but not verified (codex, gemini), `--global` refuses rather than guess a flag on a command nobody has confirmed against the real CLI; `--show` still prints the block, and picking user versus project scope is then the same manual step it always was. VS Code has no such distinction to make — every install already lands in its one user-level file, so `--global` changes nothing there and is accepted as a no-op.

## Why rta does not write client config files

Where a client ships its own command, rta runs that. Where it does not, rta prints and stops. Three reasons, in the order that decides it:

- **That file is what grants an agent access to your secrets.** A tool whose entire argument is that consent should be visible and deliberate has no business writing itself into five agents' permission files unattended.
- **Those files hold things rta must not touch.** VS Code's `mcp.json` is JSONC — comments and all — and often carries API keys in headers. A parse-and-rewrite would destroy comments at best and mishandle a credential at worst.
- **A config format changes when its client changes, not when rta does.** The tool that owns the format is the one that stays correct.

## Claude Code

```bash
rta mcp install claude
```

This runs `claude mcp add rta -- /path/to/rta mcp serve --as claude`. The `--` matters and rta always passes it: without the separator, `claude` reads `--as` as one of its own flags.

Scope is Claude Code's decision, not rta's. `claude mcp add` registers a server for the current directory alone by default, under that directory's entry in `~/.claude.json`; `--scope user` puts it there for every project, and `--scope project` writes the project's `.mcp.json` instead. `rta mcp install claude --global` passes `--scope user` through, and takes out a directory-only registration of rta for the directory you ran it in, which would otherwise override the new one there (unless that entry sets environment variables of yours, in which case it stays and the answer says so):

```bash
rta mcp install claude --global
# runs: claude mcp add rta --scope user -- /usr/local/bin/rta mcp serve --as claude
```

The answer's `scope` line says which it was, so a session in another project that has no rta is not how you find out. `rta doctor` reads the same files and reports, for every client on the machine, whether rta is registered with it.

Confirm it connected:

```bash
claude mcp list
```

## VS Code

```bash
rta mcp install vscode
```

This runs `code --add-mcp` with a JSON spec. VS Code uses its own key — `servers`, not the `mcpServers` everything else uses — which is why the printed block differs from the others.

If you would rather edit the file, open the command palette and run **MCP: Open User Configuration**, then add:

```json
{
  "servers": {
    "rta": {
      "command": "/usr/local/bin/rta",
      "args": ["mcp", "serve", "--as", "vscode"]
    }
  }
}
```

## Cursor

Cursor has no command for this, so rta prints the block:

```bash
rta mcp install cursor
```

```json
{
  "mcpServers": {
    "rta": {
      "command": "/usr/local/bin/rta",
      "args": ["mcp", "serve", "--as", "cursor"]
    }
  }
}
```

Put it in `~/.cursor/mcp.json` for every project, or `.cursor/mcp.json` for one. The per-project file is worth preferring — it means the grants an agent holds are scoped to the repository you were working in when you issued them. `rta mcp install cursor --global` names only the user-level file, for the times every-project really is what you want.

## GitHub Copilot CLI

```bash
rta mcp install copilot
```

Copilot manages MCP from an interactive prompt, so there is nothing for rta to run. Either add the printed block to `~/.copilot/mcp-config.json`, or run `copilot` and then `/mcp` and add it there.

## OpenAI Codex CLI

```bash
rta mcp install codex
```

Codex is the one client here whose configuration is TOML rather than JSON:

```toml
[mcp_servers.rta]
command = "/usr/local/bin/rta"
args = ["mcp", "serve", "--as", "codex"]
```

That goes in `~/.codex/config.toml`. rta will try `codex mcp add` first and print this if the command is missing or fails.

## Gemini CLI

```bash
rta mcp install gemini
```

Tries `gemini mcp add`, falling back to a block for `~/.gemini/settings.json`, which uses the same `mcpServers` shape Claude Desktop established.

## Any other MCP client

Anything that can launch a stdio MCP server works, including ones rta has never heard of — Windsurf, Zed, Cline, Continue, JetBrains AI, Claude Desktop, or something you wrote. There is nothing to install on rta's side; the server is just:

```bash
rta mcp serve --as <a-name-you-choose>
```

Most clients use the shape Claude Desktop established, so this is usually what goes in the file:

```json
{
  "mcpServers": {
    "rta": {
      "command": "/usr/local/bin/rta",
      "args": ["mcp", "serve", "--as", "my-client"]
    }
  }
}
```

Use an absolute path. A client launches this months from now, from a working directory you did not choose, with a `PATH` that may not be your shell's.

**Claude Desktop is not the same thing as Claude Code.** `rta mcp install claude` configures the CLI. The desktop app is a separate application with its own configuration file, and rta has no entry for it — use the block above, and find the file through the app's own settings rather than a path written here, which would go stale the first time it moved.

## What the agent can do once it is connected

**Reads that stay on this machine, and nothing else.** That holds with no flags, no config and no decisions. `rta plugin list` is where you check what that covers — the `CAN` column is the highest safety class each plugin declares, and only the `read` half of it is reachable over MCP until you issue a grant — some of it not even then:

```bash
rta plugin list
```

```
PLUGIN   CAPABILITIES   CAN           SUMMARY
agent               7   write         What AI agents asked rta for, what they got, and what is waiting on you
audit              11   read          Security hardening checks, each graded against a named OWASP/CWE control
grant              10   write         Permissions for AI agents that expire on their own
kv                 16   destructive   Encrypted local store for secrets, certificates and key files
```

So of those four, an agent reaches every `audit` check but four: the two about this machine's own setup, `audit clients` and `audit doctor`, which are never a tool, and the two that name a host, `audit web` and `audit mail`, which need a grant; and it reaches the read half of `kv`, which is names and metadata and never a value; and nothing of `agent` or `grant` at all. Those two answer to the person at the terminal whatever their safety class, and are [never a tool](./20-mcp.md#never-a-tool).

Everything else is a grant a person issues — for a plugin, one capability, or one record of it — and that is [MCP and the safety gate](./20-mcp.md) and [Grants](./30-grants.md).

## Next

- [MCP and the safety gate](./20-mcp.md) — what is exposed before you decide anything
- [Grants](./30-grants.md) — time-boxed permission for one capability
- [The record](./40-audit-trail.md) — what the agent actually asked for
