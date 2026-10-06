# Your config file

rta needs no configuration, and a machine that never has one works: the dashboard builds itself, output is `pretty` at a terminal, and an agent reaches only what needs no grant. The config file is where you change that for yourself. It is one YAML file holding preferences (the default output format, the dashboard, the colours, a plugin's own settings) and, beside them, your [profiles](./40-profiles.md) and [roles](../30-boundary/35-roles-consent-and-the-guard.md#roles-a-day-of-grants-under-one-word).

**Nothing in it grants anything.** A grant lives in its own sealed file and is issued by a person at a terminal, a plugin's approval lives in the trust record, and a credential never lives in a config file at all: `secrets:` holds the name of where one comes from. So a config file is safe to keep in a dotfiles repository, and a line in it can make rta quieter or louder but never make an agent more able.

## Where it lives

On macOS and on Linux alike:

| Where | When |
| --- | --- |
| `$XDG_CONFIG_HOME/rta/config.yaml` | When `XDG_CONFIG_HOME` is set to an absolute path |
| `~/.config/rta/config.yaml` | Otherwise |
| the file `RTA_CONFIG` names | Whenever it is set, over both of the above, which is what portable setups and test harnesses use |

`rta config path` prints the real one for your shell, so a script or an editor never has to guess it:

```bash
rta config path
```

```
/home/you/.config/rta/config.yaml
```

A relative `XDG_CONFIG_HOME` is ignored, as the convention says. Builds before this one kept the file in `~/Library/Application Support/rta` on macOS and read nothing from the new place: if you have a config left there, `rta doctor` has an `old config` row naming it and the `mv` that moves it, and every command at a terminal says so in one line until it is moved (a pipe and `-o json` stay quiet).

## Look at it

```bash
rta config
```

```
FILE ───────────────────────────────────────────────────────────────────────────────────────
file    /home/you/.config/rta/config.yaml
exists  yes, 15 lines
from    $XDG_CONFIG_HOME/rta

SETTINGS ───────────────────────────────────────────────────────────────────────────────────
╭──────────────────────┬───────────────────────────────┬────────╮
│ KEY                  │ VALUE                         │ SOURCE │
├──────────────────────┼───────────────────────────────┼────────┤
│ dashboard.columns    │ 2                             │ file   │
│ theme.primary        │ #7aa2f7                       │ file   │
│ dashboard.add        │ 1 tile — `rta dashboard list` │ file   │
│ plugins.http.timeout │ 10                            │ file   │
╰──────────────────────┴───────────────────────────────┴────────╯
```

With no file it says so and that every key is at its default. `rta config show` is the same answer under a name that says what it does. A value an environment variable outranks names the variable in the `SOURCE` column and what the file says (`RTA_OUTPUT (the file says json)`), because a file that seems ignored is the first thing worth ruling out when output is not what you wrote.

## Change one key

A key is named the way `rta explain` prints it: `output`, `dashboard.columns`, `dashboard.hidden`, `dashboard.order`, `theme.<slot>` and `plugins.<plugin>.<key>`. A setting several capabilities of a plugin share is one key (`plugins.http.timeout`), and one that a single capability reads is spelled with its name (`plugins.sys.cpu.cores`, which makes `rta sys cpu` draw every core). `rta explain http.get` lists the ones a capability reads and carries the line that sets one.

```bash
rta config set theme.primary '#7aa2f7'
rta config set dashboard.columns 2
rta config set plugins.http.timeout 10
rta config get plugins.http.timeout
rta config unset plugins.http.timeout
```

`set` answers with what it wrote and the line that undoes it:

```
wrote  set plugins.http.timeout to 10 in /home/you/.config/rta/config.yaml
back   `rta config unset plugins.http.timeout` returns it to its default, 30
```

`set` holds the value to what the key takes, so a value no call would accept is refused here and not written to be refused on every call:

```bash
rta config set plugins.http.timeout 10s
```

```
ERROR core.config.set.type plugins.http.timeout is declared int, and takes a whole number from 1 to 600
HINT write it as `rta config set plugins.http.timeout 30`
```

A key that takes a list takes one argument per element (`rta config set dashboard.hidden gen.overview git.overview`). Setting a key to what it already is writes nothing, so a line in a script that runs on every boot is safe. A name that is a block of keys and not one value (`plugins`, `theme`) is refused with the keys it holds, a misspelled key is answered with the nearest one, and `--dry-run` says what would be written without writing it.

**Your comments stay.** `set`, `unset`, `rta dashboard add`, `rta profile set` and the TUI all edit the file in place, so a note you wrote above a key is still above it afterwards, and a file with Windows line endings keeps them. Say the file holds this:

```yaml
# two columns suit my laptop; the monitor wants more
dashboard:
  columns: 3
```

After `rta config set dashboard.columns 2` the number changes and the comment does not. A comment on a line a command removes goes with it, and a note directly above a block it removes too.

If `RTA_OUTPUT` is exported and you set `output`, the answer adds a line saying so, because the variable outranks the file and commands keep printing what it says until it is unset. A credential is never printed back: `rta config get` refuses a key that holds one (`core.config.key.secret`) and `rta config show` shows `(redacted — a credential does not belong in this file)`, which is also the cue to move it into `secrets:` or the [secret store](./50-secrets.md).

## Edit it by hand

```bash
rta config edit
```

It opens the file in `$VISUAL`, then `$EDITOR`, then `vi`, and holds what you save to the same checks as `rta config check` before it replaces the file. With no file yet it opens a starter with the common keys commented out, so nothing is stated until you uncomment a line:

```yaml
# Uncomment what you want to change.
# output: json                  # the default --output: pretty, json, yaml, csv or md
#
# dashboard:
#   columns: 2                  # the grid's width; left out, it follows the terminal
#   hidden: [gen.overview]      # tiles to leave off the screen
#
# theme:
#   primary: '#D97757'          # the ten palette slots; `rta config schema` names them
#
# plugins:
#   http:
#     timeout: 10               # `rta explain http.get` names each key a plugin reads
```

A file that does not parse reopens the editor with the reason and its line at the top, and leaving it as it is cancels. A key rta ignores is saved and named, with its line. `rta config edit` also keeps `config.schema.json` beside the file and points the file's first lines at it, so an editor that speaks yaml-language-server completes each key, flags an unknown one and explains it on hover; `rta config schema` prints the same JSON Schema for a file somewhere else. It needs a terminal (without one it says so and names `rta config set` and `rta config path` instead), and an editor that returns before you have saved loses the edit, so a windowed one needs its wait flag: `EDITOR='code --wait'`.

## Check it

```bash
rta config check
```

```
╭──────┬──────────────┬────────────────────────────────────────────────────────────────────────────╮
│ LINE │ KEY          │ PROBLEM                                                                    │
├──────┼──────────────┼────────────────────────────────────────────────────────────────────────────┤
│   17 │ theme.primry │ not a color this rta understands (one of: accent, bad, faint, good, ink,   │
│      │              │ inverse, label, muted, primary, warn)                                      │
│   18 │ dashbord     │ is not a key rta reads (did you mean "dashboard"?)                         │
│   20 │ output       │ "jsno" is not an output format (it takes pretty, json, yaml, csv or md)    │
╰──────┴──────────────┴────────────────────────────────────────────────────────────────────────────╯
```

A key rta does not read is ignored, which on its own would make a typo look like a setting that does nothing, so every command at a terminal warns about one and `rta doctor` has a `config` row for each with its line. `rta config check` exits `1` when it finds something and says `has nothing rta ignores` when it does not, so it fits a pre-commit hook. A value `output:` cannot take is the one problem that stops commands: every command that renders fails as `core.output.invalid` and names the file, and `rta doctor`, `rta config` and `rta config set output pretty` still run so you can fix it.

## Split it across files

When one file grows long — a profile per environment, a team's roles, the colours — put some of it in `config.d`, the directory beside the file: `~/.config/rta/config.d/` next to `config.yaml`, or `rta.d` next to the file `RTA_CONFIG` names. Every `.yaml` or `.yml` file in it is read after the file, in the order of its name, and says the same keys the file does:

```yaml
# ~/.config/rta/config.d/20-dashboard.yaml
dashboard:
  columns: 3
  add:
  - id: kube.overview
    profile: prod
```

**Each thing is stated in one file.** A profile, a role, one plugin's settings, the `dashboard:` block, the `theme:` block and `output` each live in a single file, and stating one twice is refused, naming both files, rather than settled by an order nobody wrote down. The alternative is a later file quietly changing where `prod` points for every command that follows, which is the one thing you could not see from either file alone. It costs nothing the split is for: a team's profiles in one file, an environment's in another, the dashboard and the colours in a third.

**A write goes where the thing lives.** `rta profile set prod`, `rta dashboard add`, `rta config set` and the TUI edit the file that states what they change, with your comments kept, and put something nothing states yet in `config.yaml`. The answer names the file it wrote. `rta config` lists the drop-ins and the file each setting is in, `rta config check` checks every file, and `rta config edit 10-prod` opens one of them (a new one is created when you save), holding what you save to the same rule: a profile, role or block that another file already states is refused while you are still looking at it. A first line of `# yaml-language-server: $schema=../config.schema.json` gives a drop-in the completion the file has.

**Only a file somebody named has a directory.** The `./.rta.yaml` rta falls back to when there is no config directory has none, for the reason it has no profiles and no dashboard: a cloned repository does not get to arrange what rta connects to. Only regular files are read, or a link to one, which is what a dotfiles manager makes of this directory; a name that starts with a dot, ends in `~` or is not `.yaml` or `.yml` is left alone, and at most 64 are read. Nothing in a drop-in grants anything, any more than a line of the file does, and the plugins' own sandbox keeps the directory from them as it keeps the file.

## A first config that is good

Small, and stating only what you chose. The defaults are chosen to be the right ones (the automatic dashboard, the built-in colours, an agent that reaches only what needs no grant), and a key you state is a decision you made and a default that can no longer improve under you. A first file worth having is a few lines:

```yaml
dashboard:
  columns: 2
  add:
  - id: eol.watch
theme:
  primary: "#7aa2f7"
plugins:
  http:
    timeout: 10
```

That is a dashboard two tiles wide with the version watchlist added to it, one accent colour, and a shorter timeout for `rta http` calls. Every line of it was one command: `rta config set dashboard.columns 2`, `rta dashboard add eol.watch`, `rta config set theme.primary '#7aa2f7'` and `rta config set plugins.http.timeout 10`. What does not belong in it: a credential (use `secrets:` and [the store](./50-secrets.md)), a grant (`rta grant allow`), and a plugin's approval (`rta plugin trust`). The file says what is shown and how a connection is spelled, and the boundary says what an agent may do.

No tool on the [MCP](../95-reference/10-glossary.md#acronyms) surface writes this file, and [the path gate](../95-reference/20-the-path-gate.md) keeps the config directory out of an agent's reach: the file decides what is printed and which connections exist, so an agent that could write it could change its own limits.

## Make the dashboard and the theme yours

Two commands each, and [Dashboard and theme](./25-dashboard-and-theme.md) is the rest:

```bash
rta dashboard add eol.watch          # a tile the automatic set leaves out
rta dashboard hide git.overview      # one the automatic set draws, off your screen
rta dashboard list                   # every tile bare rta would draw, and where each came from
rta config set theme.primary '#7aa2f7'
```

In the TUI the same changes are keys: `+` adds a tile from the catalogue or a search match, `H` hides one, `[` and `]` move it, and `t` opens the colour editor. A tile you added from another terminal reaches an open dashboard on its next refresh.

## Related

- [Where rta keeps things](../95-reference/50-where-rta-keeps-things.md) — the config directory next to the data directory and every file in both
- [Profiles](./40-profiles.md) — the `profiles:` block, which `rta profile set` writes for you
- [Profiles in depth](./41-profiles-in-depth.md#editor-completion-for-the-file) — the schema, in an editor

## Next

[Dashboard and theme](./25-dashboard-and-theme.md) — stating the dashboard in the file, and the colours.
