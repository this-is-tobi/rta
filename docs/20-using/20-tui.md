# The TUI

```bash
rta
```

Bare `rta` on a terminal opens the interactive shell. In a pipe it prints help instead, so a script never hangs on an interface nobody can see, and so does a terminal that says `TERM=dumb`, which cannot redraw a screen in place.

It is the same capabilities as the CLI — the same declarations, the same safety classes, the same results. What changes is that you can browse them, fill inputs in a form, and see a table you can walk through.

## The landing dashboard

A search line across the top, and one tile per plugin that has something to show at a glance, in the order that matters on a first run: the machine (`sys`), what agents asked of it (`agent`) and the grants standing under it (`grant`), then the network, the secret store and the notebook. The search line holds the selection when the TUI opens, so a word typed into it — `password`, `dns`, `trust` — is a search over every capability in the catalogue, filtering as you type, and not a run of one-letter commands. The single letters below belong to a tile: press an arrow to select one and they are commands again. `/`, `enter`, `:`, `+` and `?` work from the line either way and `ctrl+c` quits from anywhere; `q` is a letter in the line, so quitting from a cold start is `ctrl+c`, or an arrow and then `q`. A paste into the line is a search as well, with line breaks folded to spaces. `gen.overview` and `fs.tree` qualify as tiles and are left off on purpose, because a table of freshly generated secrets is not a status and the working directory says nothing about the machine; each is one `rta dashboard add` away.

The search is one line until you use it: `/` (or `enter` on the line) opens it to a box with the first matches, and `esc` gives the lines back to the tiles. On a short terminal each row of tiles is drawn at half the room under the search line and cut at `… enter for details`, so an 80x24 screen holds the machine, the agents, the grants and the network, and `enter` opens the rest of any of them.

A tile is a glance, so it is drawn smaller than the page `enter` opens. A table too wide for its box becomes a borderless line per row under its headings. The first column stays; the columns that say the same in every row, or nothing, go first, then the mostly empty ones, then the plain ones from the last back, then the ones that say when, and the ones that grade themselves are the last of all. The column of prose is cut with an ellipsis before any of them is dropped, and a long path is cut in the middle, where its end still names the file. A tile with nothing to say from where you started rta is a muted sentence and not an error: `git.overview` outside a repository says so, and `enter` opens the error with its hint. When calls are parked waiting for you, the `agent.overview` tile says `● N waiting` in its own border, in the warning colour.

On a machine that has just been set up, at 80 columns by 24 rows, it looks like this, with your own machine's figures in the tiles:

```
 rta  dashboard  ↓ more
 ❯ 20 plugins · 128 capabilities — type to search
╭─ sys.overview ──────────────────────╮ ╭─ agent.overview ─────────────────────╮
│ host  laptop.home · darwin 26.5.2   │ │ waiting on you          0            │
│       (arm64) · up 4d 19h 5m        │ │ connected now                        │
│ cpu   10.9% of 14 cores             │ │   none — no client has an rta server │
│ mem   25.7 GiB / 36.0 GiB (71.5%)   │ │   open; `rta mcp install claude`,    │
│ swap  20.9 GiB / 22.0 GiB (95.2%)   │ │   then restart the client            │
│ load  3.05 · 3.74 · 4.56 (22%/core, │ │ locked                  nothing      │
│       ok)                           │ │ roles in force          none         │
│ … enter for details                 │ │ … enter for details                  │
╰─────────────────────────────────────╯ ╰──────────────────────────────────────╯
╭─ grant.list ────────────────────────╮ ╭─ net.overview ───────────────────────╮
│ guard  off — any process running as │ │ en0         192.168.1.20 ·           │
│ you can issue a grant               │ │             2001:db8:b23:68c0:14cd:  │
│ (grant.guard.on)                    │ │             1a1e:1cea:bc2f           │
│                                     │ │ dns         192.168.1.99 · 9.9.9.9 · │
│ No grant is standing — agents reach │ │             9.9.9.10 · 192.168.1.254 │
│ only what needs none.               │ │             · 8.8.8.8 ·              │
│ Allow one with: grant.allow         │ │             fd0f:ee:b0::1            │
│ target=<capability> ttl=15m         │ │ … enter for details                  │
╰─────────────────────────────────────╯ ╰──────────────────────────────────────╯
 type to search · ↑↓←→ select · enter search · : browse · + add tile
 p f t plugins, profiles, theme on a tile · ctrl+c quit · ? help
```

| Key | What it does |
| --- | --- |
| `/` | Search |
| `enter` | Open |
| `esc` | Back |
| `q` or `ctrl+c` | Quit |
| `?` | Every key the screen answers, aliases included — what the footer had no room for |
| `[` `]` | Move a tile |
| `H` | Hide a tile — or remove it, when it is one you added |
| `+` | Add a tile — on the dashboard it opens the catalogue; on a catalogue row or a search match it adds that one |
| `p` | Plugin inventory — where a hidden automatic tile comes back |
| `t` | Theme |
| `f` | Profiles — your configured environments, which one is on |
| `b` or `:` | Browse the whole catalogue |
| `w` | The queue of calls parked for you, while one waits |
| `c` | Copy the selected tile's value, on a tile that offers one |

Tiles are yours to arrange. `H` hides one you never look at, and `p` opens the inventory where it comes back; one panel of an entry that expanded into several connections is hidden by its own key, which the inventory has no row for, so it comes back with the `rta dashboard unhide <id> --profile <profile/instance>` line its note prints. On a tile you added, `H` removes the entry instead — the footer says `remove` there, and the note prints the `rta dashboard add` line that puts it back; `+` on a catalogue row or a search match adds one the automatic set left out, asking which connection when the capability takes one.

The selected tile is the one with the coloured border. On a terminal that shows no colour (`NO_COLOR`, `TERM=dumb`) its border is drawn in heavy lines instead, so the selection never rests on colour alone. The idle search line has no border to colour: when it is the selection its `⌕` becomes a `❯`.

What a key did is said in the footer, beside the keys: a green `✓` for something done and a red `✗` for something that did not happen, such as a save the file refused or an approval that was turned down. The mark carries the difference where colour does not show.

Stating the whole dashboard in the config file, and the colours it draws in, are on [Dashboard and theme](./25-dashboard-and-theme.md).

## Answering agents

A call parked for you shows on every screen: one line above it, `● 1 call waiting — w to answer`, naming the call when there is exactly one, for as long as it waits and until it is answered or runs out. The line takes a row from the screen below it while it is there, and none on a terminal too short to spare one. `w` opens the queue from the dashboard, with or without a tile selected and from the search bar too — where it is the one letter that is a command while a call waits — and from any screen whose letters are commands; over a form or a filter box the line says `esc, then w`. The queue is read every two seconds, whichever screen is up.

The agent tile says the same, and opens the queue and the record. From it, and from the queue:

| Key | What it does |
| --- | --- |
| `w` | The queue of parked calls |
| `g` | The record of what agents did, at once with its defaults — `e` on it changes the filters |
| `enter` | Everything about the call under the cursor, including what it would do |
| `a` | Allow it once — the footer names the call and what it would do, and `enter` allows exactly that call |
| `A` | Allow it for a while, or as a role — the `ttl` and `role` form, for the same call |
| `d` | Deny it — one key, no form |
| `L` | Lock the agent that asked, its name already filled in from the row; from the agent tile, the agent that is connected when there is only one |

`a` stops for a confirmation because granting access is the direction that cannot be taken back once a secret has been read, and what it stops on is the call itself, in one line, rather than a form that never said which call it was about. Any key but `enter` and `A` cancels it and says the call is still waiting. The line holds the call that was named when `a` was pressed, so the queue refreshing under the cursor cannot change which one `enter` allows. The form asks for the guard's passphrase only when the grant guard is on.

The grant [roster](../95-reference/10-glossary.md#terms) answers the same way. Open it from its tile — the key on the tile opens the list, with a row to stand on, and says so — and:

| Key | What it does |
| --- | --- |
| `x` | Revoke the grant under the cursor, at once, and only that grant: a row that does not name exactly one grant opens the form instead |
| `X` | Revoke every grant, behind the dry run that says how many — `enter` on it is the consent |
| `n` | Renew the grant under the cursor — a form |

On the lock list `x` lifts the lock under the cursor. The queue refreshes itself every few seconds while it is on screen, so a call that parks while you are reading appears, and one that expires leaves. A form opened from one of these screens asks only about this machine: the `--server` box that aims the same command at a remote queue, and the operator passphrase that signs it, are offered when you type the command's name, not when you act on a row.

## The catalogue

Every capability as a table grouped by plugin — one row each, with its ID, its [safety class](../95-reference/10-glossary.md#terms) and its summary. The filter stays live, every pane is bounded by the terminal and scrolls inside it, and the mouse wheel works. `enter` runs the row; `+` puts it on the dashboard.

The permission column says what an AI agent needs for the row, after its safety class: nothing more than the class (`read`) is a capability an agent can call at once, `agents need grant` is one it can call only once you have issued a grant, and `not for agents` is one it can never call — yours alone. The plugins that are all of the last kind (`agent`, `grant`, `lock`, `operator`, `pkg`) come after the others, so the first page is what you or an agent can run.

`/` opens the filter, and while its box has the keyboard `q`, `/` and `+` are letters of the query: `enter` applies it, `esc` clears it and `ctrl+c` quits. It finds IDs that start with what you typed first, then anything with every word of the query in its ID or summary, which is the dashboard search's rule, so `gen` leads with the `gen` capabilities and `hosts list` finds `net.hosts.list`.

## Running something

`enter` on a read that needs nothing runs it at once with its defaults — `sys.cpu`, `gen.password`, `time.at` — from a search match, a catalogue row, a tile's action key or an action key on a result, and `e` on the result opens the inputs when you want to change one. That is every read with nothing left to ask once the row or tile it was reached from has said what it knows, except one holding a credential nothing has supplied yet: the `kv` unlock, for one, still asks, because a run without it would only come back refused. A passphrase that only signs a request to another server is not that — `grant.list` runs, and asks for its operator passphrase when you give it a `server`. A write opens its form, because the values are the consent, and a destructive capability goes through its dry run.

A capability with a required input, or a write, opens a form built from its declaration:

- Fields that declare `Options` become a picker.
- Fields that declare `Suggest` complete from what exists on your machine — your tags, your keys, your hosts file.
- `Path` fields complete directory by directory as you type.
- `ctrl+e` opens `$EDITOR` on a long body.
- `ctrl+s` accepts every remaining field at its current value, for a form whose defaults are already right. It is the one every terminal can send; `shift+enter` does the same where the terminal reports it, and `alt+enter` where Option is set to act as Meta.

### Which environment the run goes to

A capability a profile can fill opens with the environment picker first, defaulted to whatever is switched on. Under an environment that reaches its service through a forward, the host and port boxes show the forward's own coordinate — `kube:homelab/databases/svc/postgres` in the host box, `5432` in the port box — and the picker's help line says the same: `runs through the kube forward to homelab/databases/svc/postgres:5432, which fills host, port`. Leave them and the forward answers per call. Type over one and the run connects directly to what you typed, without opening the forward — the way out of a coordinate that is wrong without leaving the form to fix the profile first. It is first because it changes what every other answer means — a host typed under one environment is not the same value under another — and moving it rebuilds the form on the environment it now names, rather than leaving one environment's values on screen under another one's name.

Boxes that environment fills open showing its values, and you can still type over them. A credential is the exception: it opens empty, because seeding a masked box paints your passphrase's length in dots. So the box says where the value comes from instead:

```
password
password for the role — staging fills it from kv:staging-db-password (secret)
>
```

**The reference, never the value.** `kv:staging-db-password` is the name of an entry — something you wrote, in a file you can read — and naming it answers the question an empty masked box could not: whether you have to type this at all. An exported `RTA_PROFILE_STAGING_PASSWORD` is named the same way, and named as the winner, because that is the one the run will actually use. A box under an environment that supplies nothing says nothing, and you type it.

**The store asks once per sitting.** A passphrase store unlocked from a TUI form stays open in this TUI for fifteen minutes past its last use — the header reads `◐ store unlocked · 14m` while it does — so the next kv action runs without the unlock form, and a profile's `kv:` reference is filled instead of failing because nothing on the update loop could ask. It lives in this process's memory and nowhere else: no file, no environment variable, nothing a plugin or an agent started from here inherits, and closing the TUI ends it. A wrong passphrase ends it, and so does a rekey. What it never covers is the reveal itself: `v` and `c` still open the unlock form, because the unlock is what makes showing a secret a deliberate act rather than a slip on a list.

### Tab means one thing on every field

**Take me forward.** What that is depends only on what the box under the cursor can still be completed to, never on which field it is:

| What the box holds | What tab does |
| --- | --- |
| something an offer extends | takes the offer, and stays — so a path, a cluster coordinate or a comma list is walked a segment at a time, exactly as in a shell |
| nothing yet | says what is on offer, because there is no ghost to accept until something is typed |
| everything there was to complete | moves to the next field |

`shift+tab` is the previous field and `enter` is the next one, whatever the box holds. A field that completes says so under itself, because the footer speaks for the screen rather than for the box under the cursor.

`↓` and `↑` browse the offer from an empty box: down puts the first candidate in the box, the next down its neighbour, up walks the other way, and both wrap. Type over what was placed and the box is yours again — the arrows then cycle the matches of what you typed, which is what they always did once a letter was in.

**A list box reads what the list flag reads.** Its elements are separated by commas, and the space around each is dropped, so `recipe, italian` is two tags. An element holding a comma, a double quote or space at an end goes in double quotes, a quote inside it written twice — `"a,b", " padded", "say ""hi"""` — and a box that opens holding a list, a note's tags or a declared default, writes it the same way, so leaving it untouched gives back the list it showed. A quote left open is refused in the footer as you type.

A destructive capability never runs from its form. What opens instead is its own dry run — what the call would do with the values you gave, the same preview an operator sees on a [parked agent call](../30-boundary/35-roles-consent-and-the-guard.md#live-consent-when-you-would-rather-be-asked) — and `enter` on that screen is the consent. `e` reopens the inputs, `esc` runs nothing. A plugin from outside the binary gets no dry run before you confirm, because rta does not run a plugin's own claim about itself before anyone has said yes; its screen shows the inputs the call will run with, and says so.

## Working with results

| Key | What it does |
| --- | --- |
| `enter` | Open the row |
| `e` | Edit inputs and run again |
| `r` | Re-run |
| `y` | Copy as JSON |
| `d` | Delete |
| `esc` | Stop a run that is still going; on a result, go back |
| `?` | Every key the screen answers — on any screen where a key is a command rather than text |

A log — `agent log`, or any table that declares its newest row last — opens on that row, scrolled to the end, so what just happened is under the cursor and the past is a key up.

Views are actionable rather than static, and what a view offers is the plugin's own declaration rather than a list the TUI keeps: each capability says which keys its result answers, what they open and what they read off the row, and `rta explain <capability>` prints the same. In the notebook, `t` turns a note into a to-do or back, `d` checks one off and `x` removes; on the consent queue, `L` opens the lock form beside the call that made you want it, and on the lock list `x` lifts the lock under the cursor — from the list *and* from a record's own page, refreshing as it goes. A third-party plugin's list gets exactly the same treatment for exactly the same declaration. Detail pages are composed from other capabilities' views rather than rebuilt, so a record page shows metadata, prose and relations as separate sections.

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

## Related

- [Dashboard and theme](./25-dashboard-and-theme.md) — stating the dashboard in the config file, and the colours
- [The plugin inventory](../40-plugins/15-the-plugin-inventory.md) — what `p` shows, and the plugins rta found and refused to run
- [The CLI](./10-cli.md) — the same capabilities, scriptable

## Next

[Seeing the shape of things](./30-trees.md) — mapping a directory, a bucket, a Vault mount or an etcd keyspace in one call.
