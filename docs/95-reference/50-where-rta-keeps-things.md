# Where rta keeps things

## Configuration

rta runs with no configuration at all, and a fresh machine needs none. What is worth doing once is connecting the agent clients that are on it, and that is what `rta init` is for:

```bash
rta init
```

It looks at the machine. For each client it finds — Claude Code, VS Code, Codex, Gemini — it offers to register rta with it, as the command it would run, one question each, Enter to skip; it prints the line that turns on tab completion for your shell, and names the command that attaches the first-party plugin index. It writes no config file, and run again it offers only what is not done yet. A client that already has a registration, with whatever options, is left as it is, and one it can run nothing for (Cursor, GitHub Copilot CLI) is named with the command that prints what to add. On a machine with none of the clients it knows it says so, because `rta mcp install <name>` prints the standard block for any other client that speaks MCP. The questions are asked on your terminal whatever stdout is, so `rta init -o json > answer.json` asks them there and leaves the answer alone in the file. On a terminal that cannot redraw (`TERM=dumb`, a serial console) each is a plain `[y/N]` line under the same text — where it registers and the command it runs — so a yes is never given blind.

`rta init --yes` registers every client it lists without asking, which is what a dotfiles script or a devcontainer wants, and `--dry-run` shows what that would run. With neither a terminal nor `--yes` it stops with exit code `3` and changes nothing.

When you do want a setting, the config file is `~/.config/rta/config.yaml` (or the platform equivalent: `rta doctor` prints the real path), and `rta config schema` describes every key. `RTA_CONFIG` overrides the location, which is what portable setups and test harnesses use.

Nothing in the config grants anything. It holds connection profiles, dashboard preferences and theme — see [Profiles](../20-using/40-profiles.md).

## The files

| What | Where | Notes |
| --- | --- | --- |
| Config | `~/.config/rta/config.yaml`, or `~/Library/Application Support/rta/config.yaml` on macOS | `RTA_CONFIG` overrides. Beside it: `policy.yaml` (your own [team policy](../30-boundary/50-team-policy.md)), `remotes.yaml` (the servers you operate) and the `kv.identity` key `kv init --generate` makes |
| Data directory | `$RTA_DATA_DIR`, else `$XDG_DATA_HOME/rta`, else `~/.local/share/rta` — on macOS as well as Linux | Everything rta writes below is in it, owner-only. `rta doctor` prints it |
| Encrypted store | `kv.age` and `kv.recipients` | [Secrets](../20-using/50-secrets.md) |
| Grants | `grants.json`, with its seal key `grants.key` | Sealed against tampering |
| Agent record | `agent-log.jsonl`, with its seal key `agent-log.key` | Hash-chained; [The record](../30-boundary/40-audit-trail.md) |
| Locks | `lockdown.json`, with `lockdown.key` | Sealed like the grants; [Locks](../30-boundary/45-stop-an-agent-now.md) |
| Switched-on profile | `profile.json` | Which profile `rta use` switched on, and until when. Not sealed, because all it can do is remove a bound: a file that goes missing leaves the grants alone deciding, which the sealed grants are the answer to; [Profiles](../20-using/40-profiles.md) |
| Plugins | `trusted.json` (what you approved), `plugins/store/` and `plugins/bin/` (what an index installed, and the links that find it), `plugin-cache/` with `plugin-cache.key` (what each plugin build declared, sealed, so a run does not start every plugin to ask), `indexes/` (the clones) | [Using plugins](../40-plugins/10-plugins.md) |
| Notebook and shortlists | `notes.json`, `recent.json` | [The CLI](../20-using/10-cli.md) |
| Team policy | `.rta-policy.yaml`, walking up from the working directory | [Team policy](../30-boundary/50-team-policy.md) |

Exact paths differ per platform. `rta doctor` prints the real ones rather than the documented ones, which is the answer to use when they disagree.

A machine whose environment names no home — a service started without `HOME`, a container run as a uid with no environment — is asked its account database first. When that has none either, rta keeps its state in `rta-<uid>` under the temporary directory, which does not outlast a reboot or a container, says so on stderr once per run, and refuses to use that directory when it is not a private directory of the account. Set `HOME` or `RTA_DATA_DIR` to keep grants, the record and the store: a state directory that vanishes with the container is an audit trail that does too. A config path that falls back to `./.rta.yaml` for want of a config directory is not honoured for profiles, plugin settings or the dashboard, which is why a container sets `RTA_CONFIG` as [the MCP chapter](../30-boundary/67-containers-and-images.md#in-a-container-for-a-hardened-server) shows.

## Related

- [Installation](../10-getting-started/10-installation.md) — putting rta on your `$PATH`
- [The path gate](./20-the-path-gate.md) — why an agent cannot read most of these
