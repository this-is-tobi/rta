# The TUI

```bash
rta
```

Bare `rta` on a terminal opens the interactive shell. In a pipe it prints help instead, so a script never hangs on an interface nobody can see.

It is the same capabilities as the CLI — the same declarations, the same safety classes, the same results. What changes is that you can browse them, fill inputs in a form, and see a table you can walk through.

## The landing dashboard

A search bar across the top, and one tile per plugin that has something to show at a glance. Typing filters every capability in the catalogue on the fly.

| Key | |
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

Destructive capabilities confirm before acting, the same as on the CLI.

## Working with results

| Key | |
| --- | --- |
| `enter` | Open the row |
| `e` | Edit inputs and run again |
| `r` | Re-run |
| `y` | Copy as JSON |
| `d` | Delete |
| `esc` | Leave a slow run |

Views are actionable rather than static. In a task list, `d` marks done and `x` removes — from the list *and* from a record's own page, refreshing as it goes. Detail pages are composed from other capabilities' views rather than rebuilt, so a record page shows metadata, prose and relations as separate sections.

## Profiles

`f` opens the profiles pane — your configured environments, which one is on, and what each covers.

| Key | |
| --- | --- |
| `f` | Open profiles |
| `u` | Use this one |
| `n` | New |
| `d` | Delete |
| `s` | Set a credential |

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
