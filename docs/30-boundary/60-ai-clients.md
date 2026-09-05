# Connecting your AI tool

rta is an MCP server over stdio, so anything that speaks MCP can use it. This chapter is the per-client detail: where each one keeps its configuration, what `rta mcp install` will and will not do for it, and how to check afterwards that it actually worked.

If you only read one thing, read this: **the `--as` name is the whole point.** Without it every MCP client on your machine is one principal, so consent you give while talking to one follows all the others. `rta mcp install` always passes it, and the default is the client's own name.

## The short version

```bash
rta mcp install claude
```

That is it for a client that ships its own configuration command. For one that does not, the same command prints exactly what to add and where, and writes nothing.

## What rta does for each client

| Client | `rta mcp install` | Configuration file | Verified |
| --- | --- | --- | --- |
| Claude Code | runs `claude mcp add` | `.mcp.json`, or `~/.claude.json` | ✅ against the real CLI |
| VS Code | runs `code --add-mcp` | VS Code's user `mcp.json` | ✅ against the real CLI |
| Cursor | prints the block | `~/.cursor/mcp.json`, or `.cursor/mcp.json` per project | block only — Cursor has no CLI for this |
| GitHub Copilot CLI | prints the block | `~/.copilot/mcp-config.json` | block only — Copilot configures MCP from its own `/mcp` prompt |
| OpenAI Codex CLI | runs `codex mcp add` | `~/.codex/config.toml` | ⚠️ command declared, not verified |
| Gemini CLI | runs `gemini mcp add` | `~/.gemini/settings.json` | ⚠️ command declared, not verified |

The "verified" column is honest rather than reassuring. Two of these commands were written from their documented interface and have not been run against the tool itself, so if one has moved, rta falls back to printing the block instead of failing — which is the whole reason the fallback exists.

`--show` prints the block without running anything, for any client:

```bash
rta mcp install codex --show
```

## rta will not write another tool's config file

Where a client ships its own command, rta runs that. Where it does not, rta prints and stops. Three reasons, in the order that decides it:

- **That file is what grants an agent access to your secrets.** A tool whose entire argument is that consent should be visible and deliberate has no business writing itself into five agents' permission files unattended.
- **Those files hold things rta must not touch.** VS Code's `mcp.json` is JSONC — comments and all — and often carries API keys in headers. A parse-and-rewrite would destroy comments at best and mishandle a credential at worst.
- **A config format changes when its client changes, not when rta does.** The tool that owns the format is the one that stays correct.

## Claude Code

```bash
rta mcp install claude
```

This runs `claude mcp add rta -- /path/to/rta mcp serve --as claude`. The `--` matters and rta always passes it: without the separator, `claude` reads `--as` as one of its own flags.

Scope is Claude Code's decision, not rta's. `claude mcp add` writes to the project's `.mcp.json` by default; `--scope user` puts it in `~/.claude.json` for every project. Pass it through:

```bash
claude mcp add --scope user rta -- /usr/local/bin/rta mcp serve --as claude
```

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

Put it in `~/.cursor/mcp.json` for every project, or `.cursor/mcp.json` for one. The per-project file is worth preferring — it means the grants an agent holds are scoped to the repository you were working in when you issued them.

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

## Checking it worked

Three things, in order of how much they tell you.

```bash
rta doctor
```

The row worth reading is not about clients at all — it is whether your secret store unlocks without a passphrase in this environment. If it does, an MCP server started here can open it, bounded by grants but able to. `doctor` also reports whether the `claude` CLI is on your `PATH`; the other clients are not probed, because rta only shells out to that one to check.

Then ask the agent to call something harmless. `sys.overview` is a read, needs no grant, and reaches nothing off the machine — it appears to the agent as the tool `sys_overview`, since MCP tool names cannot carry a dot. If it comes back, the wiring is good.

Then look at what actually happened:

```bash
rta agent log --limit 20
```

Every call is there with the name you registered the client under. If the agent name is not what you expect, the client is running an rta you did not configure — an old absolute path, or a second install.

### Nothing shows up

`rta agent overview` has a `connected now` row, and it is the one to read first: it names every client that has an rta server open right now, by the name it was registered under, with how many calls each has made. `--detail` adds a table with what the client called itself, since when, its session id, the directory it started in and the record file it writes to. From there the silence is one of three things:

- **Connected, zero calls.** The wiring is fine and the agent has not chosen an rta tool yet. Clients with many servers attached pick by tool description, and one that already has a Kubernetes or database server will often reach for that one first. Ask for something only rta answers — `rta agent overview` itself, or a capability under a profile — and the calls appear.
- **Not connected.** Claude Code's `claude mcp add` registers rta for the current directory by default, so a session opened in another directory has no rta server at all. `rta doctor` says which it is — `every project`, `this project`, or `this directory only` — and prints the `--scope user` command that makes it global.
- **Connected, calls made, and the record is empty.** The server and the TUI are reading different data directories. The server prints `record: <path>` when it starts, in the client's MCP log; `rta agent overview --detail` shows the file the TUI reads. A `RTA_DATA_DIR` or `XDG_DATA_HOME` set in one shell profile and not the other is the usual cause.

Several sessions under the same name are one principal — grants and the team ceiling apply to `claude`, not to a window — and the record tells them apart by session: each server has an id, shown on the detail page and as a column in `rta agent log`, and `rta agent log --session <id>` narrows to one.

## Two clients, two sets of permissions

This is what `--as` buys, and it is worth doing deliberately:

```bash
rta mcp install claude --as claude-work
rta mcp install cursor --as cursor-scratch
```

Now a grant naming one does not reach the other:

```bash
rta grant allow pg.query --agent claude-work --profile staging --ttl 1h
```

`cursor-scratch` still cannot run `pg.query`, whatever it asks for. Without the names, that one grant would have covered both.

The name is your word, not the agent's. A client announces itself in the MCP handshake and rta records that claim, but it does not authorize on it — a name a thing chooses for itself is not an identity. What authorizes is the name you typed. You will see both in the record: the agent name plainly, the client's self-report in parentheses.

## What the agent can do once it is connected

**Read capabilities only.** That holds with no flags, no config and no decisions. `rta plugin list` is where you check what that covers — the `CAN` column is the highest safety class each plugin declares, and only the `read` half of it is reachable over MCP until you say otherwise:

```bash
rta plugin list
```

```
PLUGIN   CAPABILITIES   CAN           SUMMARY
agent               7   write         What AI agents asked rta for, what they got, and what is waiting on you
audit               5   read          Security hardening checks, each graded against a named OWASP/CWE control
grant               4   write         Time-boxed permissions for AI agents
kv                 13   destructive   Encrypted local store for secrets, certificates and key files
```

So of those four, an agent reaches all of `audit` and the read half of the others, and nothing in `kv` that returns a secret.

Everything else is a separate decision — a namespace at a time for writes, one capability at a time for destructive ones, and grants for anything narrower. That is [MCP and the safety gate](./20-mcp.md) and [Grants](./30-grants.md).

## Next

- [MCP and the safety gate](./20-mcp.md) — what is exposed before you decide anything
- [Grants](./30-grants.md) — time-boxed permission for one capability
- [The record](./40-audit-trail.md) — what the agent actually asked for
