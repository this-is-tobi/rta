# The TUI

```bash
rta
```

Bare `rta` on a terminal opens the interactive shell. In a pipe it prints help instead, so a script never hangs on an interface nobody can see.

It is the same capabilities as the CLI — the same declarations, the same safety classes, the same results. What changes is that you can browse them, fill inputs in a form, and see a table you can walk through.

## The landing dashboard

A search bar across the top, and one tile per plugin that has something to show at a glance. Typing filters every capability in the catalogue on the fly.

| Key | What it does |
| --- | --- |
| `/` | Search |
| `enter` | Open |
| `esc` | Back |
| `q` or `ctrl+c` | Quit |
| `[` `]` | Move a tile |
| `H` | Hide a tile |
| `p` | Plugin inventory — where a hidden tile comes back |
| `t` | Theme |
| `c` | Configure |

Tiles are yours to arrange. `H` hides one you never look at; `p` opens the inventory where any of them comes back.

## The catalogue

Every capability as a table grouped by plugin — one row each, with its ID, its safety class and its summary. The filter stays live, every pane is bounded by the terminal and scrolls inside it, and the mouse wheel works.

## Running something

A capability with inputs opens a form built from its declaration:

- Fields that declare `Options` become a picker.
- Fields that declare `Suggest` complete from what exists on your machine — your tags, your keys, your hosts file.
- `Path` fields complete directory by directory as you type.
- `ctrl+e` opens `$EDITOR` on a long body.
- `shift+enter` accepts every remaining field at its current value, for a form whose defaults are already right.

### Tab means one thing on every field

**Take me forward.** What that is depends only on what the box under the cursor can still be completed to, never on which field it is:

| What the box holds | What tab does |
| --- | --- |
| something an offer extends | takes the offer, and stays — so a path, a cluster coordinate or a comma list is walked a segment at a time, exactly as in a shell |
| nothing yet | says what is on offer, because there is no ghost to accept until something is typed |
| everything there was to complete | moves to the next field |

`shift+tab` is the previous field and `enter` is the next one, whatever the box holds. A field that completes says so under itself, because the footer speaks for the screen rather than for the box under the cursor.

Destructive capabilities confirm before acting, the same as on the CLI.

## The plugin inventory

`p` opens what is installed, what each plugin puts on the dashboard, and — the part worth having a pane for — **any artifact rta found on `$PATH` and refused to run**. A trust gate's failure mode is silence: a plugin that is installed and doing nothing looks exactly like one that was never installed.

| Key | What it does |
| --- | --- |
| `p` | Open the inventory (and close it) |
| `space` | Show or hide its dashboard tile |
| `t` | Approve an artifact, or take an approval back |
| `c` | Configure it |
| `enter` | Its capabilities, in the search bar |

`t` is the decision made where the evidence is: the digest and the path are on the screen while you take it, which the command line shows you only afterwards. Neither direction takes effect on the process you are in — trust is read once, before anything is launched — so approving says it loads when rta restarts, and withdrawing says the plugin already running stays running until rta exits.

## Working with results

| Key | What it does |
| --- | --- |
| `enter` | Open the row |
| `e` | Edit inputs and run again |
| `r` | Re-run |
| `y` | Copy as JSON |
| `d` | Delete |
| `esc` | Leave a slow run |

Views are actionable rather than static. In a task list, `d` marks done and `x` removes — from the list *and* from a record's own page, refreshing as it goes. Detail pages are composed from other capabilities' views rather than rebuilt, so a record page shows metadata, prose and relations as separate sections.

## Profiles

`f` opens the profiles pane — your configured environments, which one is on, and what each covers. `n` creates one and `c` edits it, which is the shortest way to define a profile: the form is generated from each plugin's declared inputs, and it knows which of them are secrets, so a credential lands in `secrets:` as a reference instead of in `set:` as a value.

| Key | What it does |
| --- | --- |
| `f` | Open profiles |
| `u` | Use this one |
| `enter` | The plugins inside it |
| `n` | New |
| `d` | Delete |
| `s` | Set a credential |
| `y` | Copy `export` lines for the credentials nothing has set — shown only when there are some |

**Adding a plugin to an environment is one form.** Press `n` in the pane and the editor opens on the plugin under your cursor, already pinned to its artifact, with that plugin's own configuration keys under it — `tab` completes the name, and naming a different one swaps the keys below. The form asks three things and says where each starts:

```
── how to reach it — one of these, or neither to connect directly ────
── what staging changes about it ────
```

If the plugin you added needs a credential and nothing supplies one, the credential editor is the next screen rather than somewhere to navigate back to. `esc` declines; the entry is already saved.

Switching a profile on here does the same thing `rta use` does, including the part worth remembering: while a profile is on, `rta mcp serve` refuses every other one. See [Profiles](./40-profiles.md).

## Charts

Some capabilities render as charts when there is a terminal to draw in:

```bash
rta sys cpu --cores
rta net ping example.com --graph
```

Markdown bodies — notes, `audit` findings, anything returning prose — are rendered rather than dumped.

## Untrusted plugins

If rta found an `rta-plugin-*` binary it has not been told to run, the TUI says so in a pane rather than a startup line. The line would be written to the primary buffer, and the TUI opens on the alternate one — so it would be covered before anyone could read it. The pane is the only place a person inside the TUI can learn a decision is pending.

See [Using plugins](./50-plugins.md#trust).

## Next

- [The CLI](./10-cli.md) — the same capabilities, scriptable
- [Profiles](./40-profiles.md) — what the `f` pane manages
