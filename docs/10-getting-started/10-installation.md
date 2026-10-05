# Installation

rta runs on macOS and Linux, on both `amd64` and `arm64`; [Supported platforms](#supported-platforms) lists where the two differ. It is one binary with nothing else to install first.

## Install a release

Every release ships a prebuilt binary for macOS and Linux on both `amd64` and `arm64`, as `rta_<version>_<os>_<arch>.tar.gz`, plus `.deb`/`.rpm`/`.apk` packages for Linux. With the [gh CLI](https://cli.github.com):

```bash
gh release download --repo this-is-tobi/rta \
    --pattern "rta_*_$(uname -s | tr A-Z a-z)_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz" \
    --pattern checksums.txt
```

Or with nothing but curl — the asset names carry the version, so ask the API which one is latest first:

```bash
tag=$(curl -s https://api.github.com/repos/this-is-tobi/rta/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
os=$(uname -s | tr A-Z a-z); arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -fsSLO "https://github.com/this-is-tobi/rta/releases/download/v${tag}/rta_${tag}_${os}_${arch}.tar.gz"
curl -fsSLO "https://github.com/this-is-tobi/rta/releases/download/v${tag}/checksums.txt"
```

Check what you downloaded before you extract it: [Verify a download](../95-reference/30-verify-a-download.md) takes a minute and proves the archive was built by this repository's release workflow from the tagged commit. Then put the binary on your `$PATH`:

```bash
tar -xzf rta_*.tar.gz rta
install -m 0755 rta /usr/local/bin/rta    # or ~/.local/bin, anywhere on $PATH
rta --version
```

On a Debian, RPM or Alpine machine the package does the `$PATH` half for you: download the matching `.deb`, `.rpm` or `.apk` the same way and hand it to `dpkg -i` / `rpm -i` / `apk add --allow-untrusted`.

Upgrading is the same download again — the new binary replaces the old one, and your config, store and grants live in your own directories, untouched (see [Where rta keeps things](../95-reference/50-where-rta-keeps-things.md)). Run `rta doctor` after an upgrade: when a release starts refusing a config value an older one accepted, doctor names each such value before a command runs into it, and the release notes list those changes under breaking changes.

## Check that it works

```bash
rta --version
rta doctor
```

`doctor` reports what rta can see and, more usefully, what it can *reach*, with the rows that need you first: `error`, then `warn`, then `info`, then `ok`. On a machine that has never run rta these are the rows that matter, abridged:

```
CHECK            STATUS  DETAIL
grant guard      info    off — anything that can run commands as you can issue a grant; `rta grant guard on` puts a passphrase in front of that
capabilities     ok      20 plugins, 128 capabilities
agent grants     ok      none active — agents cannot write or destroy anything
locks            ok      none — nothing is frozen
kv store         ok      none yet — `rta kv init --generate` sets one up
```

An `info` row is a note: a fact worth reading, not a failure, and a line under the table counts them and names the ones that bear most on what an agent can reach. Read the `info` rows rather than skipping to the failures. After you set a store up with `rta kv init --generate`, the `kv store` row changes to the one worth knowing about before you connect an agent:

```
kv store         info    unlocks from this environment (<your key file>) — an MCP server started here can read secrets, bounded only by grants
```

That is a real statement about what an agent started from this shell inherits. [Connect an agent](./30-connect-an-agent.md) says what to do about it, and `rta doctor` is worth running again whenever something behaves oddly. `rta doctor --detail` gives the rows that keep part of what they say to a line all of it.

The exit status is `0` unless a row is an `error`, when it is `1`; `rta doctor --strict` fails on a `warn` too, so a dotfiles check, a pre-commit hook or a CI step can gate on it, and the report is printed first either way.

## Supported platforms

rta is built for macOS and Linux, on `amd64` and `arm64`, and the two systems differ in the places this table lists. The rest behaves the same on both, apart from what the host itself reports: `sys` reads the machine it runs on, and `pkg` drives the package managers that machine has.

| | macOS | Linux |
| --- | --- | --- |
| Packages | none: the release archive | a `.deb`, `.rpm` or `.apk` as well as the archive |
| Plugin confinement | applied: each plugin runs under `sandbox-exec`, with rta's own state and your credential locations denied | none: a plugin runs with your user's access, and `rta doctor` says `none on linux`; process groups and the environment allowlist still apply |
| A port forward a killed rta left behind | stopped by the next rta that starts | ended by the kernel along with the server that started it |
| Copying a value (`kv copy`) | `pbcopy` | `wl-copy`, `xclip` or `xsel`, whichever the session has |
| Desktop notifications (`--consent-notify`) | `osascript` | `notify-send` |
| The system plugin root, where an image or a package puts plugins for everyone | none until `RTA_SYSTEM_DIR` names one | `/usr/local/lib/rta` unless `RTA_SYSTEM_DIR` says otherwise |

There is no native Windows build. The Linux build runs under the Windows Subsystem for Linux, version 2, which has not been tested, so expect the Linux column there.

## Shell completion

Completion covers subcommands, allowed values and what actually exists on your machine; [The CLI](../20-using/10-cli.md#completion) says how far it goes.

```bash
# zsh — a directory you own, put on fpath before compinit runs
mkdir -p ~/.zsh/completions
rta completion zsh > ~/.zsh/completions/_rta
# once, in ~/.zshrc, above the compinit line:
#   fpath=(~/.zsh/completions $fpath)

# bash — bash-completion loads this directory on its own
mkdir -p ~/.local/share/bash-completion/completions
rta completion bash > ~/.local/share/bash-completion/completions/rta

# fish
rta completion fish > ~/.config/fish/completions/rta.fish
```

Restart your shell afterwards. Homebrew users can write the zsh file to `$(brew --prefix)/share/zsh/site-functions/_rta` instead, which is already on `fpath`.

The recipe every tool's own help prints, `rta completion zsh > "${fpath[1]}/_rta"`, is worth avoiding: `fpath[1]` is whatever directory happens to be first on your machine, and it is often one you cannot write. kitty's shell integration, for one, puts its own completions directory there, owned by root, and the command fails with "permission denied".

## Other ways to install

[Other ways to install](./40-other-ways-to-install.md) has building from source, the container image and the Helm chart. The plugins that talk to a service, such as `pg`, `redis`, `s3` and `vault`, are separate from the binary and arrive from an index: [Using plugins](../40-plugins/10-plugins.md#getting-the-first-party-ones) is the two commands. Some capabilities shell out to a tool you already have, and [External tools](../95-reference/40-external-tools.md) says which.

## Next

[Quick start](./20-quickstart.md) — ten minutes: ask a question, open the shell, connect an agent, refuse a call and allow it.
