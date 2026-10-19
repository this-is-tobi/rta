# The TUI

```bash
rta
```

Bare `rta` on a terminal opens the interactive shell. In a pipe it prints help instead, so a script never hangs on an interface nobody can see, and so does a terminal that says `TERM=dumb`, which cannot redraw a screen in place.

It is the same capabilities as the CLI — the same declarations, the same safety classes, the same results. What changes is that you can browse them, fill inputs in a form, and see a table you can walk through.

## The landing dashboard

A search line across the top, and one tile per plugin that has something to show at a glance, in the order that matters on a first run: the machine (`sys`), what agents asked of it (`agent`) and the grants standing under it (`grant`), then the network, the secret store and the notebook. The search line holds the selection when the TUI opens, and the keys below are commands from there as they are from any tile: `p`, `f` and `t` open the panes, `q` quits. The search takes the keyboard when it is asked for — `/` from anywhere, or `enter` on the line — and from then on a word typed into it — `password`, `dns`, `trust` — is a search over every capability in the catalogue, filtering as you type, and not a run of one-letter commands. A paste into the line is a search without either, with line breaks folded to spaces. `gen.overview` and `fs.tree` qualify as tiles and are left off on purpose, because a table of freshly generated secrets is not a status and the working directory says nothing about the machine; each is one `rta dashboard add` away. An added `gen.overview` tile rolls its secrets when the TUI starts and when you press `r`, not on a timer, since a value that changes every few seconds cannot be read or copied before it is gone.

The search is one line until you use it: `/` (or `enter` on the line) opens it to a box that suggests what to type — `try: password · dns · trust · or an ID such as sys.cpu` — and shows the matches as you type, and `esc` gives the lines back to the tiles. The box shows three matches on a short terminal and a fifth of the terminal's height on a tall one, up to ten, and says `1/57` in its border when there are more matches than it shows, so a match that is not on screen is a known thing and not a missing one. On a short terminal each row of tiles is drawn at half the room under the search line and cut at `… enter for details`, so an 80x24 screen holds the machine, the agents, the grants and the network, and `enter` opens the rest of any of them. Where there is no room for even one clipped row, no tile is drawn and one muted line says `terminal too small, enlarge it to see tiles`, losing whole words from its end on a narrower terminal; the header's pointer to the rows off the screen, `↓ more`, gives up its word and then its arrow before the terminal can cut it through. A word that names a first-party plugin you have not installed — `pg`, `postgres`, `vault` — is answered under the line with the command that installs it, the sentence the command line gives for the same word. A plugin that is on this machine and is not running is answered the way the command line answers it: one waiting for approval is told to be approved with `rta plugin trust`, and one that failed to start is told that `rta plugin list` says why, neither is sent to an install that finds it already there. The answer keeps its line when other plugins' capabilities fill the list, so a whole ID such as `qdrant.collection.list` is not answered with the nearest names of other plugins alone. The words for the two panes a first run needs — `plugin`, `install`, `profile`, `environment`, singular or plural — are answered with where they are, because plugins and profiles are panes and not capabilities: `esc`, then `p` or `f`, or the command that does the same from a shell.

A tile is a glance, so it is drawn smaller than the page `enter` opens. A table too wide for its box becomes a borderless line per row under its headings. The first column stays; the columns that say the same in every row, or nothing, go first, then the ones the plugin marked as optional, the least wanted of them first, then the mostly empty ones, then the plain ones from the last back, then the ones that say when, and the ones that grade themselves are the last of all. The column of prose is cut with an ellipsis before any of them is dropped, and a long path is cut in the middle, where its end still names the file. A column that grades is cut the same way before it goes, to ten cells, with its first word — the verdict — still in its colour, so one host that did not answer does not take the grade of the others off the tile; and where the box cannot hold both the prose and that grade, the prose is the one that goes. A tile with nothing to say from where you started rta is a muted sentence and not an error: `git.overview` outside a repository says so, and `enter` opens the error with its hint. An empty list that names one of its own keys — the note tile's `press a to add one` — says to select the tile first until it is selected, because the keys of a tile answer on the selection and nothing answers them before that. The `kv.overview` tile counts what the store holds when the environment can open it, and otherwise says `warn — locked` and that the count could not be read, and does not ask for what opens it: `U` unlocks the store for this session, and `enter` opens the page, whose sentences name the other ways. The `agent.overview` and `kv.overview` tiles open with a `status` row, `ok`, `warn — …` or `fail — …`, and say a word where the terminal and the page `enter` opens say a sentence: `connected now` is `none` and `unlocks here` is `yes`. While the store is unlocked in this TUI the tile says so, without using the unlock, and `enter` shows the counts; the tile is re-run the moment the unlock starts or ends rather than at its own thirty-second pace, so it never disagrees with the header's `store unlocked` badge. A tile that has lost touch with its source says so instead of passing its last answer off as current. The TUI keeps each tile's last good answer for the session; when a run fails, or the answer is older than twice the tile's pace, the tile draws that answer dimmed, with no colour on it, and says when it was current in the right of its top border: `as of 14:02 · refresh failed` after a failure, `as of 14:02` when it is merely old, with the day when it is not today's, and in shorter words on a tile too narrow for them. A failure with no good answer before it is the error, as it always was; a tile paused for a refused credential keeps `paused — credentials refused`; and a connection that changes (the environment switched, a profile edited) takes the kept answer away, so one environment's numbers are never drawn under another's name. The failure still counts: the border stays red and a folded section's header still says `● 1 bad`. A tile whose capability declares a pace of an hour or more (`eol.watch`, `pkg.overview`, `audit.deps`) is not run when the TUI opens, because each launch would run an [OSV](../95-reference/10-glossary.md#acronyms) lookup per dependency, a handshake per host and a kubectl audit at once; it is run when its first pace has elapsed or when you press `r` on it, which the footer offers, and until then it says `not run yet — r runs it` in the muted colour. A tile with a shorter pace runs at launch. A tile's border takes the grade of its whole answer, the way a folded section's header does: amber when something in it is a warning, red when a status or usage cell is in the red, the run failed, or the page says its backend did not answer, and the quiet grey otherwise — also when the row that says so is below the tile's cut. The selected tile keeps the primary colour, and on a terminal that shows no colour the words in the cells carry the grade. When calls are parked waiting for you, the `agent.overview` tile says `● N waiting` in its own border, in the warning colour. A tile whose credential is refused twice in a row stops running — `pg.auth.failed` or `pg.denied`, any code ending in `.auth.failed` or `.denied`, and never a timeout or a refused connection, which a tile has to keep asking to say the service is back. Asked every few seconds, a wrong password is an account locked on a database that locks. The tile says `paused — credentials refused` with the refusal beside it, still counts as a bad tile in a folded section's header, and runs again when you press `r` on it or when the environment or profile it reads changes.

On a machine that has just been set up, at 80 columns by 24 rows, it looks like this, with your own machine's figures in the tiles:

```
 rta  dashboard  ↓ more
 ❯ 21 plugins · 131 capabilities — press / or enter to search
╭─ sys.overview ──────────────────────╮ ╭─ agent.overview ─────────────────────╮
│ status  ok                          │ │ status          ok                   │
│ disk    ok — 41% of / (380.1 of     │ │ waiting on you  0                    │
│         926.4 GiB)                  │ │ needs grant     0                    │
│ mem     ok — 25.7 of 36.0 GiB       │ │ connected now   none                 │
│ load    ok — 22%/core               │ │ last hour       0                    │
│ cpu     10.9% of 14 cores           │ │ last call       nothing recorded     │
│ swap    20.9 of 22.0 GiB (95%)      │ │                 yet                  │
│ host    laptop.home · up 4d 19h     │ │ … enter for details                  │
╰─────────────────────────────────────╯ ╰──────────────────────────────────────╯
╭─ grant.list ────────────────────────╮ ╭─ net.overview ───────────────────────╮
│ guard  off — any process running as │ │ status   ok                          │
│ you can issue a grant               │ │ route    via 192.168.1.254 on en0    │
│ (grant.guard.on)                    │ │ traffic  rx 8.8 · tx 25.5 KiB/s      │
│                                     │ │ dns      192.168.1.99 (+5 fallback)  │
│ No grant is standing — agents reach │ │ hosts    5 custom                    │
│ only what needs none.               │ │ ip       192.168.1.20                │
│ Allow one with: grant.allow         │ │                                      │
│ target=<capability> ttl=15m         │ │                                      │
╰─────────────────────────────────────╯ ╰──────────────────────────────────────╯
 ↑↓←→ select · enter search · f profiles · p plugins · t theme · V view
 + add tile · U unlock store · b browse · q quit · ? help
```

| Key | What it does |
| --- | --- |
| `/` | Search, from wherever the selection is. Once the box is open, letters are the query and `+` adds the highlighted match as a tile |
| `tab` `shift+tab` | Select the next tile, or the one before it, which from the first tile is the search bar |
| `home` `end` | Select the first tile or the last, from the search bar or from any tile — the header of a section where one opens or closes the dashboard, since a folded section holds no tile to rest on |
| `enter` | Open — on the search line, start the search — or fold and unfold a [section](./25-dashboard-and-theme.md#sections-group-tiles-and-fold-the-group), on its header (`space` does the same there) |
| `esc` | Back |
| `q` or `ctrl+c` | Quit |
| `?` | Every key the screen answers, aliases included — what the footer had no room for. On a terminal with fewer rows than the list has lines it takes two columns, so none is cut |
| `[` `]` | Move a tile — or the whole section, with its header selected |
| `H` | Hide a tile — or remove it, when it is one you added |
| `z` | Fold or unfold the section of the selected tile or header. For this session only — nothing is written |
| `Z` | Fold every section, or unfold them all when none is open |
| `V/v` | Draw the next screen of the dashboard with `V` and the one before with `v`: the screens the environment that is on [links](./25-dashboard-and-theme.md#views-a-screen-per-environment), in the order it lists them, and no others — or the default and then each view by name when it links none or none is on. Both go round at the ends, so `v` from the first screen draws the last, and either undoes the other. For this session only — nothing is written — and switching environment undoes it |
| `U` | Unlock the encrypted store for this sitting, and lock it again after asking: a masked box for the passphrase (the key's own, for a store locked to a passphrase-protected key) and one for how long to keep it open, from a minute to twelve hours — the store asks once per sitting, below |
| `+` | Add a tile — on the dashboard it opens the catalogue; on a catalogue row or a search match it adds that one |
| `p` | Plugin inventory — where a hidden automatic tile comes back |
| `t` | Theme — a choice of whose palette (yours, or one profile's; the environment that is on is already chosen), then one box per colour with the palette it produces drawn above |
| `f` | Profiles — your configured environments, which one is on |
| `b` or `:` | Browse the whole catalogue |
| `w` | The queue of calls parked for you, while one waits |
| `c` | Copy the selected tile's value, on a tile that offers one |
| `r` | Ask a tile that has stopped on a refused credential again, or run one that was left for its first pace (`not run yet`) — offered only on those |

The mouse wheel scrolls the tiles a row at a time. When the window leaves the selected tile the selection moves to the nearest row that is drawn, so `enter`, `H` and `[` `]` never act on a tile that is off the screen.

Tiles are yours to arrange. `H` hides one you never look at, and `p` opens the inventory where it comes back; one panel of an entry that expanded into several connections is hidden by its own key, which the inventory has no row for, so it comes back with the `rta dashboard unhide <id> --profile <profile/instance>` line its note prints. On a tile you added, `H` removes the entry instead — the footer says `remove` there, and the note prints the `rta dashboard add` line that puts it back, in its section and its view as `rta dashboard rm` prints it; `+` on a catalogue row or a search match adds one the automatic set left out, asking which connection when the capability takes one.

A dashboard that groups its tiles draws each group under a header row, `▾ databases ──── 5 tiles · ok`, and a long one is where that earns its place: the panels of an entry that expanded into several connections are a group of their own, `pg · staging`, and the header says the worst thing its tiles say even when they are folded away. [Sections](./25-dashboard-and-theme.md#sections-group-tiles-and-fold-the-group) say how to state them.

The header is somewhere the selection rests, as a tile is: the arrows go to it, `enter`, `space`, `z` or a click on its line fold it or unfold it, and the arrows pass over the tiles of a folded section, which are not drawn. `z` and `Z` are on the bar only when the dashboard has a section, and on a dashboard that has none they are not keys at all. `[` `]` on a header move the section past whatever is beside it; on a tile of a section that holds others they move the tile among them, and at the end of the section they say so rather than do nothing — a tile does not change section by being moved, since the section is part of its entry. `H` on a header says so too, and writes nothing: it takes one tile down at a time, and a section is several that are taken down in different ways. A fold is not written, so it does not follow you into the next terminal; `collapsed:` in the config is how a section starts.

The selected tile is the one with the coloured border. On a terminal that shows no colour (`NO_COLOR`, `TERM=dumb`), or when you ask for none with `rta --no-color`, its border is drawn in heavy lines instead, so the selection never rests on colour alone. The idle search line has no border to colour: when it is the selection its `⌕` becomes a `❯`. The chosen answer of a yes/no box in a form is the same: its fill is the colour, so without colour it is drawn between brackets, `[ yes ]     no`.

What a key did is said in the footer, beside the keys: a green `✓` for something done, a red `✗` for something that did not happen, and a `→` for what to do next. A save the file refused is a `✗`, and its line leads with `not saved` and says the change holds for this session only; so are an approval that was turned down and a key with nothing to act on, such as `t` on a built-in plugin or `U` with no store. The mark carries the difference where colour does not show.

**The colours follow the environment that is on.** A profile can have a colour and a `theme:` of its own ([a palette per environment](./25-dashboard-and-theme.md#a-palette-per-environment)), and the TUI draws in them: `u` on the profiles pane repaints everything, a result on screen included, and `rta use` from another terminal, a deadline lapsing and an edit of the profile in force reach it on the next refresh. Switching off puts your own colours back.

The config file the dashboard and the colours are stated in is [Your config file](./22-your-config-file.md), and stating the whole dashboard there, with the colours it draws in, is on [Dashboard and theme](./25-dashboard-and-theme.md).

## Asking first

A key that costs something to undo asks before it acts, in a box over the dimmed screen, above the key bar that names its keys: a title that is the question, a sentence saying what will happen, and two buttons named for what they do. `U` on an unlocked store is the first, `Lock the store?`, and `esc` or `ctrl+c` on a form where something differs from what it opened with is the second, `Discard what you typed?`; the answer is `enter` on the focused button, `y` for the first button, `n` or `esc` to cancel, `tab` or the arrows to move the focus, or a click on a button. While the box is up nothing underneath hears a key, a scroll or a paste, and a stray key does not dismiss it; `ctrl+c` still quits. The focus starts on the answer that loses nothing: the lock itself, since locking is the direction that narrows what is open and a reflex `enter` should land there, and `Keep editing` on the form's question, since discarding is not. No setting turns the question off. The questions are few and each costs something, and a setting that removed a consent step would be writable by anything running as you.

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
| `L` | Lock the agent that asked, its name already filled in from the row and named in the form's title (`lock agent claude`), and in the footer once it is done (`locked agent claude`); from the agent tile, the agent that is connected when there is only one |

`a` stops for a confirmation because granting access is the direction that cannot be taken back once a secret has been read, and what it stops on is the call itself, in one line, rather than a form that never said which call it was about. Any key but `enter` and `A` cancels it and says the call is still waiting. The line holds the call that was named when `a` was pressed, so the queue refreshing under the cursor cannot change which one `enter` allows. The form asks for the guard's passphrase only when the grant guard is on. Both answers leave a receipt in the footer that names the call, `allowed claude's note.rm 3 once` or `denied claude's note.rm 3`, because the row leaves the queue as soon as it is answered.

The grant [roster](../95-reference/10-glossary.md#terms) answers the same way. Open it from its tile — the key on the tile opens the list, with a row to stand on, and says so — and:

| Key | What it does |
| --- | --- |
| `x` | Revoke the grant under the cursor, at once, and only that grant: a row that does not name exactly one grant opens the form instead |
| `X` | Revoke every grant, behind the dry run that says how many — `enter` on it is the consent |
| `n` | Renew the grant under the cursor — a form |

On the lock list `x` lifts the lock under the cursor. The queue refreshes itself every few seconds while it is on screen, so a call that parks while you are reading appears, and one that expires leaves. A form opened from one of these screens asks only about this machine: the `--server` box that aims the same command at a remote queue, and the operator passphrase that signs it, are offered when you type the command's name, not when you act on a row.

## The catalogue

Every capability as a table grouped by plugin — one row each, with its ID, its [safety class](../95-reference/10-glossary.md#terms) and its summary. The filter stays live, every pane is bounded by the terminal and scrolls inside it, and the mouse wheel works. `enter` runs the row; `+` puts it on the dashboard. `pgup` and `pgdn` move a page and `home` and `end` go to the first row and the last. Once a filter is applied the header says `filter: pass · 9 of 131`, and `esc` clears it before a second `esc` goes back.

The permission column says what an AI agent needs for the row, after its safety class: nothing more than the class (`read`) is a capability an agent can call at once, `agents need grant` is one it can call only once you have issued a grant, and `not for agents` is one it can never call — yours alone. The plugins that are all of the last kind (`agent`, `grant`, `lock`, `operator`, `pkg`) come after the others, so the first page is what you or an agent can run.

The list cuts a summary to what the ID and the permission leave it, which is seventeen cells at 80 columns, so the row under the cursor is said in full on the line above the bar: its whole summary, and after it what an agent needs, as a sentence (`read · an agent can call it at once`, `write · an agent needs a grant you issue first`, `write · never for an agent — yours alone`) when the whole of it fits, in the column's own words when only those do, and not at all when neither does — the column beside the row says it. A terminal too short to leave the list twelve rows with the line takes the line away rather than a row of the list.

`/` opens the filter, and while its box has the keyboard `q`, `/` and `+` are letters of the query: `enter` applies it, `esc` clears it and `ctrl+c` quits. It finds what every word of the query names in its ID, its keywords or its summary, best first, and an ID's own words come before a sentence's: `gen` leads with the `gen` capabilities, `ports` leads with `net.port` and not with the command whose summary says "exports", and `hosts list` finds `net.hosts.list`. A query of four letters or more that finds nothing is offered what it most likely misspells, so `cpuu` shows `sys.cpu`. It is the dashboard search's rule, which is also the rule `rta explain` and an unknown command's hint use.

## Running something

`enter` on a read that needs nothing runs it at once with its defaults — `sys.cpu`, `gen.password`, `time.at` — from a search match, a catalogue row, a tile's action key or an action key on a result, and `e` on the result opens the inputs when you want to change one. That is every read with nothing left to ask once the row or tile it was reached from has said what it knows, except one holding a credential nothing has supplied yet: the `kv` unlock, for one, still asks, because a run without it would only come back refused. A passphrase that only signs a request to another server is not that — `grant.list` runs, and asks for its operator passphrase when you give it a `server`. A write opens its form, because the values are the consent, and a destructive capability goes through its dry run.

A capability with a required input, or a write, opens a form built from its declaration:

- Fields that declare `Options` become a picker. `/` filters it, and `esc` clears the filter before it cancels the form, so what you typed in the boxes above is not lost to a filter you were done with.
- A box that must be answered says so on its title line, `url · required`, in words that show on a terminal with no colour. Under the title is the field's own help, and after it a word for the shape only where the shape narrows what the box takes: `int`, `duration`, `path`, `secret`, `list, comma-separated`, `multi-line`.
- The form's head says on the right what class of call it is when it is above a read, `write` or `destructive`, and where the cursor is among the boxes, `3 of 10`. A form of one box has no length to give, and a read says no class.
- The call's own inputs come first. The connection your config states — a host, a port, a database, how to verify the server, and the credential the environment supplies — comes after them in blocks, so a database's schema and limit are not eleven boxes down from where the form opens. Each block is named on one row, the first row of its first box, so the name is on screen when the cursor enters the block: `network` for where the service is, `tls` for how it is verified, `credentials` for who is asking, `target` for what is picked inside it, or a name the plugin gave its own. A form of nothing but connection is blocked the same way, and a limit or a prefix that your config gives a default is the call's own and stays where it was. The cursor starts on the first box that has to be answered and holds nothing (`path` on `vault.kv.get`, not the optional `mount` declared before it), and on the first box when there is none.
- A box no value fills shows the value a capability's first example gives it, `e.g. Go, macOS`, muted and led by those words so it still reads as an example with no colour. It is a placeholder and never an answer: `enter` on it submits nothing for the box, and a required box is still refused as empty.
- A box that shows a default takes the first character you type as the answer in place of it, so a timeout showing `5` becomes `9` and not `59`, and `now` becomes the zone you typed and not `nowEurope/Paris`. Any other key edits what is there — backspace, an arrow, `home`, `end`, a paste — and a box you have touched is an ordinary box. Only a default is replaced: a value from the run `e` reopened, or from the record being edited, is yours to edit. A box left showing its default still answers nothing, so the call takes the default from where it belongs.
- Fields that declare `Suggest` complete from what exists on your machine — your tags, your keys, your hosts file.
- `Path` fields complete directory by directory as you type.
- `ctrl+e` opens `$EDITOR` on a long body.
- A capability that edits a record, `kv.set` or `note.edit`, asks in two pages: which record, then its contents, seeded with what it holds. The second page names the record in its head, `key mykey`, and on the first the fast key says `next`, since it only moves on to the second.
- `enter` moves to the next field, and on the last one does what finishing the form does, which the footer says in its place: `enter run` for a call, `enter preview` for a destructive one, which shows what it would do before anything happens, and `enter save` for an editor.
- A refusal leads the footer and is wrapped across its two rows, the hints giving way to it lowest first and how to leave last. One too long for the width loses its middle, marked `…`, and keeps its end, which is where it says what to do about it.
- `esc` cancels the form and `ctrl+c` quits, and where something differs from what the form opened with, typed or chosen and not put back, each asks `Discard what you typed?` first; from the question `ctrl+c` quits. A form nobody touched closes at once, and so does one whose only change is the environment picker, which is a choice of where the call goes and not something typed. A form `e` opened from a result goes back to the result when discarded.
- `ctrl+s` accepts every remaining field at its current value, for a form whose defaults are already right. It is the one every terminal can send; `shift+enter` does the same where the terminal reports it, and `alt+enter` where Option is set to act as Meta.

### Which environment the run goes to

A capability a profile can fill opens with the environment picker first, defaulted to whatever is switched on. Under an environment that reaches its service through a forward, the host and port boxes show the forward's own coordinate — `kube:homelab/databases/svc/postgres` in the host box, `5432` in the port box — and the picker's help line says the same: `runs through the kube forward to homelab/databases/svc/postgres:5432, which fills host, port`. Leave them and the forward answers per call. Type over one and the run connects directly to what you typed, without opening the forward — the way out of a coordinate that is wrong without leaving the form to fix the profile first. It is first because it changes what every other answer means — a host typed under one environment is not the same value under another — and moving it rebuilds the form on the environment it now names, rather than leaving one environment's values on screen under another one's name.

**The list is the environment that is switched on.** With one on (`rta use`), the picker lists the base configuration, that environment's connections, the entry that states one for the session, and last `— other environments (4) —`, the number of connections of other environments that choosing it would add. A screenful of production targets while you are working in the homelab is one keystroke from the wrong one, so they wait behind the entry. `enter` on it builds the same form again with every environment listed, the one that is on first and the rest in their own order, the cursor on the first connection of another environment and what you typed kept; the list then ends with `— only homelab —`, which goes back. The entries ask when you choose them, with `enter` or `tab`, and the cursor passing over them on the way down asks nothing. The choice is held until the TUI quits, like a connection stated for the session, and it is one switch for every form: the next form opens as wide as this one is. Nothing is written. With no environment switched on there is nothing to narrow to, and the picker lists every environment as it always did. A choice made earlier is not hidden: a form opened from a result, by `e` or from one of its rows, opens on the connection that run went through, a tile's pinned connection included, and when that is another environment's it expands the list for itself and leaves the switch where it was.

**This is a display and not a boundary.** It keeps the wrong environment out of reach of a reflex and nothing else. A grant is for the profile it names, what an agent may reach is held to the environment that is switched on by the code that runs the call, and neither reads what a picker lists; the other environments are one entry away on purpose, and the profiles pane (`f`) lists all of them.

Boxes that environment fills open showing its values, and you can still type over them. A credential is the exception: it opens empty, because seeding a masked box paints your passphrase's length in dots. So the box says where the value comes from instead:

```
password
password for the role — staging fills it from kv:staging-db-password (secret)
>
```

**The reference, never the value.** `kv:staging-db-password` is the name of an entry — something you wrote, in a file you can read — and naming it answers the question an empty masked box could not: whether you have to type this at all. An exported `RTA_PROFILE_STAGING_PASSWORD` is named the same way, and named as the winner, because that is the one the run will actually use. A box under an environment that supplies nothing says nothing, and you type it.

**The store asks once per sitting.** When a profile's `kv:` reference points at a store that needs a passphrase, its tiles fail at every tick with a hint to press `U`. `U` opens a masked box for the passphrase — the store's, or for a store locked to an SSH key that has a passphrase of its own, that key's, which `ssh-agent` cannot supply — and one for how long to keep the store open (an hour unless you say otherwise, from a minute to twelve hours; shorter is safer). A wrong passphrase is refused and not kept. Answering returns you to the dashboard with the failing tiles run again at once, and the header reads `◐ store unlocked · 59m` while it holds. `U` again asks `Lock the store?` and, on `Lock store`, forgets the passphrase on the spot (this is not `rta lock`, which freezes an agent, and it does not touch the grant guard or a server an agent started); it locks itself when the time is up. The deadline is fixed: tiles reading a credential every few seconds do not extend it, and only a kv action you run yourself moves it by the length you chose. A passphrase store unlocked from the form of a kv action works the same way, for fifteen minutes past its last use. It lives in this process's memory and nowhere else: no file, no environment variable, nothing a plugin or an agent started from here inherits, and closing the TUI ends it. Nothing in an [MCP](../95-reference/10-glossary.md#acronyms) server or a CLI command can start one, so an agent cannot unlock the store for itself. A wrong passphrase ends it, and so does a rekey. What it never covers is the reveal itself: `v` and `c` still open the unlock form, because the unlock is what makes showing a secret a deliberate act rather than a slip on a list.

### A connection for the session

A capability with an address a forward can fill lists `— ad hoc connection… —` after the environments, and it is there with no environment configured at all, since that is when it is wanted. With nothing else to pick the cursor starts on the first box that has to be answered, as it does in every form, and the picker is one `shift+tab` away, listed, which the footer says as `⇧tab environment` on every box below it; with an environment configured the picker is the question and the cursor starts on it. Choosing the entry means pressing `enter` on it, and the cursor passing over it on the way to another entry asks nothing. Choosing it asks for what `--kube` and `--secret` state on the command line ([Reaching private services](./42-reaching-private-services.md#once-without-writing-a-profile)): the coordinate of a Service or Pod, `context/namespace/kind/name:port`, and for each credential the plugin declares a reference to it — `kube:<secret>/<key>` from a Secret in the coordinate's namespace, or `kv:<entry>` from the local store. `tab` completes the coordinate a segment at a time from the cluster, as it does in a profile, and a `kube:` reference from the Secrets of the namespace the coordinate names; listing a Secret's keys reads the Secret, so it happens on the key press and never while typing. The credential box takes a reference and never the value: a value typed there is refused where it is typed, and the refusal does not repeat it.

The run form then comes back on the connection, named `ad hoc · through staging/databases/svc/postgres:5432` in the picker, with the host and port boxes showing the forward and the credential box saying `ad hoc fills it from kube:postgres-app/password`. The result says where the call went, beside the time — and when a box typed over the coordinate sent the call straight to what was typed, it says `direct, no forward` and not `through`. The connection is held until the TUI quits, **for the plugin it was stated for**, and offered by the picker of every capability of that plugin that can take it: a coordinate names one Service and a Service speaks one plugin's protocol, so a Postgres connection is never offered to a Redis form that happens to have a password too. The replication, the activity and a table are one statement and not three. Choosing the entry again edits it; replacing it keeps the pages that named the old one working on the new. `esc` on the form goes back to the run form, on the environment it was on, and emptying every box and pressing enter drops the connection. Nothing is written: not to the config file, not to a profile, not to the store, and nothing outlives the TUI. `rta profile set` keeps one that turns out to be worth keeping.

**A person's act, and nobody else's.** The connection exists here and as flags on the command line, and on no [MCP](../95-reference/10-glossary.md#acronyms) tool, so an agent cannot name a cluster, a Service or a Secret: over MCP a connection is a profile, named by something a person consented to. It reads no `RTA_PROFILE_*` variable, since a variable exported for a profile called `ad-hoc` would otherwise spell the same name and reach a call aimed somewhere else. What it does not ask about is the rest of the connection — a user, a database — which are the run form's own boxes, as they are the capability's own flags on the command line, and a credential that is not a Secret input, which `--secret` on the command line can still map.

### Tab means one thing on every field

**Take me forward.** What that is depends only on what the box under the cursor can still be completed to, never on which field it is:

| What the box holds | What tab does |
| --- | --- |
| something an offer extends | takes the offer, and stays — so a path, a cluster coordinate or a comma list is walked a segment at a time, exactly as in a shell |
| nothing yet | says what is on offer, because there is no ghost to accept until something is typed |
| everything there was to complete | moves to the next field |

`shift+tab` is the previous field and `enter` is the next one, whatever the box holds; below a run form's environment picker, where `↑` is the box's own, `shift+tab` is the way up to it, and the footer says `⇧tab environment`. A field that completes says so under itself, because the footer speaks for the screen rather than for the box under the cursor.

`↓` and `↑` browse the offer from an empty box: down puts the first candidate in the box, the next down its neighbour, up walks the other way, and both wrap. Type over what was placed and the box is yours again — the arrows then cycle the matches of what you typed, which is what they always did once a letter was in. A comma list is browsed one item at a time, from where an item begins: after `recipe, ` the arrows walk the candidates for the next item, leaving out the ones the box already lists.

**A list box reads what the list flag reads.** Its elements are separated by commas, and the space around each is dropped, so `recipe, italian` is two tags. An element holding a comma, a double quote or space at an end goes in double quotes, a quote inside it written twice — `"a,b", " padded", "say ""hi"""` — and a box that opens holding a list, a note's tags or a declared default, writes it the same way, so leaving it untouched gives back the list it showed. A quote left open is refused in the footer as you type.

A destructive capability never runs from its form. What opens instead is its own dry run — what the call would do with the values you gave, the same preview an operator sees on a [parked agent call](../30-boundary/35-roles-consent-and-the-guard.md#live-consent-when-you-would-rather-be-asked) — and `enter` on that screen is the consent. `e` reopens the inputs, `esc` runs nothing. A plugin from outside the binary gets no dry run before you confirm, because rta does not run a plugin's own claim about itself before anyone has said yes; its screen shows the inputs the call will run with, and says so.

## Working with results

| Key | What it does |
| --- | --- |
| `enter` | Open the row |
| `e` | Edit inputs and run again; `esc` on that form comes back to this result, an error included |
| `r` | Re-run |
| `y` | Copy as JSON |
| `d` | Delete |
| `pgup/pgdn` or `space` | A page up or down an answer taller than the pane: `space` and `f` page down, `b` pages up |
| `ctrl+u` or `ctrl+d` | Half a page up or down, as `u` and `d` do where the view has no key of its own on them |
| `home` or `end` | The top of the answer and the bottom — on a list, the first row and the last, with the cursor |
| `esc` | Stop a run that is still going; on a result, go back |
| `q` or `ctrl+c` | Quit |
| `?` | Every key the screen answers — on any screen where a key is a command rather than text |

An answer taller than the pane says how far down it is in the top border, beside the time: `0%` at the top, `100%` at the bottom, and nothing at all when it fits. The footer teaches `pgup/pgdn page` only then, and `?` lists the rest.

A log — `agent log`, or any table that declares its newest row last — opens on that row, scrolled to the end, so what just happened is under the cursor and the past is a key up.

Views are actionable rather than static, and what a view offers is the plugin's own declaration rather than a list the TUI keeps: each capability says which keys its result answers, what they open and what they read off the row, and `rta explain <capability>` prints the same. In the notebook, `t` turns a note into a to-do or back, `d` checks one off and `x` removes; on the consent queue, `L` opens the lock form beside the call that made you want it, and on the lock list `x` lifts the lock under the cursor — from the list *and* from a record's own page, refreshing as it goes. A third-party plugin's list gets exactly the same treatment for exactly the same declaration. Detail pages are composed from other capabilities' views rather than rebuilt, so a record page shows metadata, prose and relations as separate sections.

## Profiles

`f` opens the profiles pane — your configured environments, which one is on, and what each covers. `n` creates one and `c` edits it, which is the shortest way to define a profile: the form is generated from each plugin's declared inputs, and it knows which of them are secrets, so a credential lands in `secrets:` as a reference instead of in `set:` as a value. Adding a plugin to an environment refuses a name that is not registered, as `rta profile set` does, and says the command that fixes it — `pg is a first-party plugin — rta plugin install pg installs it` — and the form stays open on what you typed.

An environment that runs otherwise than its file states is marked `warn`, and the reason is under its name in the warning colour, whole and wrapped to the pane, so a band is taller by the lines it takes and no taller when it has none. `enter` opens the plugins inside it, where the same reason leads with the value to write instead, above the line that says where each credential comes from.

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

- [Your config file](./22-your-config-file.md) — where it lives and the commands that change it
- [Dashboard and theme](./25-dashboard-and-theme.md) — stating the dashboard in the config file, and the colours
- [The plugin inventory](../40-plugins/15-the-plugin-inventory.md) — what `p` shows, and the plugins rta found and refused to run
- [The CLI](./10-cli.md) — the same capabilities, scriptable

## Next

[Your config file](./22-your-config-file.md) — what the file is, where it lives, and the commands that change it.
