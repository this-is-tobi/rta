# Installation

> **No packaged release yet.** rta is pre-1.0 and builds from source. The release matrix is wired and verified (darwin, linux and windows on amd64 and arm64), so the tagged builds below will appear — until then, `go install` is the path.

## From source

You need Go 1.26 or newer.

```bash
git clone https://github.com/this-is-tobi/rule-them-all.git
cd rule-them-all
make install
```

That runs `go install ./cmd/rta`, putting `rta` in `$(go env GOPATH)/bin`. If that directory is not on your `$PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

To build into the current directory instead of installing:

```bash
make build      # ./rta
```

## Verify

```bash
rta --version
rta doctor
```

`doctor` is worth running now and worth running again whenever something behaves oddly. It reports what rta can see and — more usefully — what it can *reach*:

```
CHECK                STATUS  DETAIL
capabilities         ok      16 plugins, 98 capabilities (16 built in)
config               ok      ~/.config/rta/config.yaml
kv store             info    unlocks from this environment — an MCP server
                             started here can read secrets, bounded only by grants
plugin confinement   ok      sandbox-exec: 2 paths denied read+write, 10 denied read
agent log            ok      the record is intact
```

Those `info` rows are not noise. "The store unlocks from this environment" is a real statement about what an agent started from this shell inherits, and it is the kind of thing worth knowing before you connect one.

## Shell completion

rta's completion is not just subcommands — capabilities that declare `Options` complete to their allowed values, and `Suggest` inputs complete from what actually exists on your machine (your tags, your keys, your hosts file).

```bash
# zsh
rta completion zsh > "${fpath[1]}/_rta"

# bash
rta completion bash > /etc/bash_completion.d/rta

# fish
rta completion fish > ~/.config/fish/completions/rta.fish
```

Restart your shell afterwards.

## The `ai` build

There is a second build carrying an inbound AI engine (`rta ai`), which costs about 19 MB:

```bash
make build-ai
```

It is deliberately opt-in and deliberately not the default. rta is the secured capability layer *other people's* agents call; being one more AI CLI is not the product. Unless you specifically want `rta ai`, use the ordinary build.

## Configuration

rta runs with no configuration at all. When you want some:

```bash
rta init
```

That writes `~/.config/rta/config.yaml` (or the platform equivalent — `rta doctor` prints the exact path). `RTA_CONFIG` overrides the location, which is what portable setups and test harnesses use.

Nothing in the config grants anything. It holds connection profiles, dashboard preferences and theme — see [Profiles](./40-profiles.md).

## Where rta keeps things

| What | Where | Notes |
| --- | --- | --- |
| Config | `~/.config/rta/config.yaml` | `RTA_CONFIG` overrides |
| Encrypted store | beside the config | [Secrets](./30-secrets.md) |
| Grants | `~/.local/share/rta/grants.json` | Sealed against tampering |
| Agent record | beside the grants | Hash-chained; [The record](./22-audit-trail.md) |
| Team policy | `.rta-policy.yaml`, walking up from the working directory | [Team policy](./23-team-policy.md) |

Exact paths differ per platform. `rta doctor` prints the real ones rather than the documented ones, which is the answer to use when they disagree.

## Next

- [Quick start](./02-quickstart.md) — the first ten minutes
- [MCP and the safety gate](./20-mcp.md) — if you came here to connect an agent
