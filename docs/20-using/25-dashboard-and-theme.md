# Dashboard and theme

The dashboard builds itself, and most people never touch it: [The TUI](./20-tui.md) covers hiding, moving and adding a tile with a key. This page is for stating it in the config file, so it is the same on every machine you set up, and for the colours the TUI and the CLI draw in.

## Stating the dashboard yourself

With no `dashboard:` block, rta builds one: a tile per plugin that has a capability which is `Read`, needs no input, and is cheap enough to run unasked. Plugins installed later appear on their own.

There are three ways to change that, and the difference between the first two and the third is whether tomorrow's plugin still shows up.

**Adjust the automatic set.** `hidden:` and `order:` bend it without freezing it:

```yaml
dashboard:
  hidden:
  - git.overview
  order:
  - sys.overview
  - note.list
  width: 4
```

**Add to it.** `add:` joins tiles to the automatic set, and it is the only way to get a capability the automatic dashboard leaves out. Anything that reaches off the box — every `kube`, `pg`, `s3` and `vault` capability — is kept off it deliberately, however cheap it looks: a dashboard runs its tiles on load and again on a timer, and nobody expects opening a TUI to spend an API quota or disclose anything to a third party. An entry here is you asking for it, which is a decision the automatic path can't make for you. Three surfaces write the same entry: this block, `rta dashboard add` from a shell, and `+` in the TUI on a catalogue row (`b`) or a search match (`/`), which asks which connection the tile is about when the capability takes one — a profile, one of its labelled instances, or the switch — and lands on the dashboard with the new tile selected. `+` refuses what `add` refuses, in the same words, and on an automatic tile `H` took off it shows the tile again rather than writing a twin. A tile added from another terminal reaches an open dashboard on its next refresh.

```yaml
dashboard:
  add:
  - id: eol.watch
  - id: kube.overview
    profile: prod
  - id: kube.overview
    profile: staging
  - id: pg.overview
    profile: staging/analytics
    width: 6
```

`profile:` pins a tile to one connection — a profile, or `name/instance` for one of several connections to the same plugin — and the tile is about that connection whatever `rta use` switched on, with the name on its panel. That is what lets one capability sit on the dashboard twice, once per cluster. Without it a tile follows the switched-on environment: switch to staging and the pg tile is about staging. `with:` fills the capability's inputs. `width:` states how wide it is, in twelfths of the screen — `6` is half, for the one you actually read.

**One entry, one panel per connection.** A profile can hold several connections to the same plugin — `pg/orders`, `pg/analytics`, three more — and a tile whose profile names no instance becomes one panel per connection that profile holds for its plugin, each named on its panel. `{id: pg.overview, profile: staging}` is every Postgres database staging knows, and one added to the profile next month gets its panel on its own, the same rule the automatic set follows for a plugin installed next month. A tile that follows the switch expands the same way, into whatever environment is on: under staging it is staging's databases, under prod that environment's own. Name the instance, `profile: staging/analytics`, for one panel. This is the one place a bare profile over several connections is not refused with "your call", as `--profile staging` is on a single command: a dashboard is not a choice, showing every one is the glance, and no wrong pick is possible.

The same from a script, or without opening the file:

```bash
rta dashboard add kube.overview --profile prod
rta dashboard add kube.overview --profile staging
rta dashboard add pg.overview --profile staging                # one panel per pg connection staging holds
rta dashboard add pg.overview --profile staging/analytics --width 6
rta dashboard add cert.expiry --set targets=example.com
rta dashboard list                                             # every panel bare rta would draw, where each came from, and what is hidden
rta dashboard hide pg.overview --profile staging/orders        # that one panel; its siblings stay
rta dashboard unhide pg.overview --profile staging/orders
rta dashboard rm kube.overview --profile staging
```

`add` refuses what the file would have quietly got wrong: a capability that is not a read, an input it does not declare, a credential under `--set`, a `--set` value of a type the input cannot hold (`core.dashboard.set.type`) or outside its options or range (`core.input.option`, `core.input.range` — the codes a run would refuse it with, on every refresh), a required input nothing fills, a profile that does not cover the plugin. Adding the same tile again replaces it, so the command is safe in a script that runs on every boot. In the TUI, `H` on an added tile withdraws its entry rather than hiding the capability — hiding by name would take both kube tiles down — and the footer says the command that puts it back, the same line `rm` prints: the whole entry, its section and the view it was on included; `H` on one panel of an entry that expanded hides that connection's panel by its key and leaves the rest, and `unhide` is the way back. `hide` and `unhide` are what `H` and the inventory pane do, from a script. Adding a capability the automatic dashboard already shows makes the entry that tile: `rta dashboard add sys.overview --width 6` widens it rather than doubling it, and the bare form is refused, with the way back when the tile is hidden. `[` and `]` move an entry that expanded as one, its panels together, because the file can place the entry and nothing finer.

**Or state it exactly.** `tiles:` replaces the automatic set outright — `hidden:` and `order:` are not consulted, because the list is already both, except for a `hidden:` key naming one panel of an entry that expanded, which is a panel and not an entry the list could drop. Its entries take the same `profile:`, `with:`, `width:`, `height:` and `refresh:`, and `add:` entries follow the list:

```yaml
dashboard:
  tiles:
  - id: kube.overview
    profile: prod
  - id: note.list
    width: 6
```

Worth knowing what a tile costs before you add one: it refreshes on a timer for as long as the TUI is open, so a cluster-wide `kube.overview` tile is a round of `kubectl` calls at every refresh, for every hour the terminal stays open. `rta explain kube.overview` prints what a capability actually reads, which is not always only what its name suggests, and its `dashboard` row says how often a tile of it re-runs.

## Width, height and pace: one rule at three levels

A tile is sized and paced by three keys, `width:`, `height:` and `refresh:`, and each means the same thing wherever it is written: on a tile, on a section under `sections:`, and on the screen, which is the `dashboard:` block or a view. The nearest one wins, tile then section then screen then what the tile gets by itself. The one on a section or the screen is a default for the tiles under it, and it yields to a tile that needs more room or runs at a slower pace than it asks for; the one on a tile is a demand. So `width: 6` on a section is half the screen for every tile in it, and one line replaces a `width:` on each of them.

### How wide and how tall

The grid is twelve tracks wide, as the web's is, and `width:` is a number of them: `12` is the whole row, `6` half, `4` a third, `3` a quarter. Twelve because it divides: every layout of a row is widths that add to twelve, and the terminal's own width never changes what was meant, only how many cells that is. A tile is never drawn narrower than it can be read, so a width that does not fit a narrow terminal is read as the least that does (half the screen at 80 cells, a third from 120), and the same file draws taller on a small terminal instead of cramped. With no width stated a tile has the width the terminal makes of it — two to a row from 80 cells, three from 120, four from 160 — and a capability that needs more room (`MinWidth`) takes it.

`height:` is how tall a tile's box is, in lines, borders included. Left out, it is what the tile has to say, from six lines to eleven, and a number lifts that cap for the tile with more to show: `height: 14` on a `cnpg.status` tile shows the rows that otherwise end at "… enter for details". A row is as tall as the tallest of its tiles, and a stated height is held to the room the screen has. It takes `4` to `60`.

```yaml
dashboard:
  width: 6                # two to a row, unless a section or a tile says otherwise
  sections:
    databases:
      width: 4            # three to a row
      height: 14
  add:
  - id: cnpg.status
    profile: prod
    section: databases
  - id: kube.overview
    profile: prod
    width: 12             # a whole row
```

`rta dashboard add cnpg.status --profile prod --width 4 --height 14` writes the same entry, `rta config set dashboard.width 6` the screen's, and `rta dashboard sections` prints what each section states. `columns:` and `span:`, which counted columns of a grid the terminal sized, are gone: `rta config check` names them and says what to write, since the same number would otherwise have meant something else (`span: 2` was two of four columns and is two twelfths).

### How often a tile runs

A tile runs when its screen opens and again at its pace, and where nothing says otherwise the pace follows what a run costs. One that reads this machine runs every five seconds, the dashboard's own tick and the fastest any tile goes. One that reaches a configured connection — a cluster, a database, a bucket, a vault — runs every thirty seconds, because each run is a round trip to a service somebody else logs: a `pg` tile is two `kubectl` calls and a login, and behind a bastion that audits every Kubernetes request each one is audit events for as long as the screen is drawn. A run that has not answered is never started again on top of itself, so a slow service is asked once, not once per tick.

A capability can declare a pace of its own, and that one is a floor. `eol.watch`, `eol.check` and `eol.products` re-run every two hours, `pkg.overview`, `pkg.outdated` and `pkg.os` every hour, `kube.overview` every minute and `kv.overview` every thirty seconds, because a run costs a request per product, a dozen subprocesses, five cluster-wide lists or a decrypt and nothing they report moves faster. The capability knows what its run costs and rta does not, so a configuration can make a declared pace slower and never faster: `rta dashboard add eol.watch` is the version watchlist on your landing screen at a pace endoflife.date would not notice, and it stays that way.

`refresh:` states a pace yourself, and the same key means the same thing in three places: on a tile, on a section under `sections:`, and on the screen, which is the `dashboard:` block or a view. The nearest one wins, tile then section then screen then the capability's own. A section's or a screen's value is a default for the tiles under it and yields to a slower pace a capability declared without a word, since it was never about that tile; a tile's own is a demand, and `rta config check` says when it cannot be met and what the tile will do instead.

```yaml
dashboard:
  refresh: 1m
  sections:
    databases:
      refresh: 30s
  tiles:
  - id: cnpg.status
    profile: prod
    section: databases
  - id: pg.status
    profile: prod/orders
    section: databases
    refresh: 10s
  - id: eol.watch
    refresh: 6h
```

A value takes a unit, `30s`, `5m`, `1h30m`, and at most `24h`; nothing below the five-second tick is possible and nothing below a declared pace is accepted. `rta dashboard add pg.status --profile prod/orders --refresh 10s` writes the same entry and refuses a pace the capability cannot run at, `rta config set dashboard.refresh 1m` sets the screen's, and `rta dashboard list` prints each tile's pace with where it was stated, `every 10s (tile)`, and `rta dashboard sections` each section's. Switching environments re-runs every tile that follows the switch, since its inputs just changed, and leaves a pinned tile inside its pace, since its did not.

Two things the block will not do, whatever you write in it:

- **A tile that is not `Read` is dropped.** Otherwise `{id: kv.rm, with: {key: old-token}}` would delete that key on startup and keep deleting it — on a timer, with no form and no confirmation, since the destructive gate lives on the CLI and the browse path and a tile goes through neither.
- **The whole block is ignored unless the config is one you named** — your user config directory, or `RTA_CONFIG`. rta falls back to `./.rta.yaml` when there is no user config directory (ordinary under `env -i`, in a container, in CI), and a cloned repository does not get to arrange your screen: `{id: http.get, with: {url: …}}` there would be a beacon that starts the moment you open the TUI in that directory. `hidden:` is the same hazard pointed the other way — it can take the agent tile off the screen, and that tile is where you notice a parked consent request before its clock runs out. `rta config check` says the block is ignored, `rta dashboard views` says none of the views it lists is drawn, and `V` has no views to cycle through.

## Sections: group tiles, and fold the group

A dashboard with several connections per plugin gets long: one entry over a profile that holds five Postgres connections is five panels, and the next profile is five more. A **section** draws tiles together under one header row, and the header folds them to that row, so the screen is as long as you want it to be.

A tile is in a section when its entry names one, and `sections:` says how each starts out and how wide it draws:

```yaml
dashboard:
  sections:
    databases:
      collapsed: true   # starts folded to its header row
    edge:
      width: 4          # its tiles are a third of the screen wide
  add:
  - id: pg.overview
    profile: staging/orders
    section: databases
  - id: pg.overview
    profile: staging/analytics
    section: databases
  - id: kube.overview
    profile: prod
    section: edge
```

A section is drawn together, at the place of its first tile: the tiles that name it come together there, in the order they have, and `order:` and `[` `]` place the section by placing that tile. A tile that names no section keeps its place and is drawn as it always was, with no header — a dashboard that states no section, and has no entry that expanded into several panels, draws exactly as it did before sections. A view and a profile's own screen take the same two keys, each its own: they replace the block while they are drawn, `sections:` included.

**An entry that becomes several panels is a section.** `{id: pg.overview, profile: staging}` over the five connections staging holds is one section named `pg · staging`, unless the entry names a `section:`, which all its panels then share. An entry that yields one panel is in none. The automatic name is an ordinary name in `sections:` — `"pg · staging": {collapsed: true}` starts that group folded — and a tile that follows the switch is named after whatever environment is on, `pg · staging` under staging and `pg · prod` under prod.

**The header says what a fold hides.** `▾` is an open section and `▸` a folded one, followed by its name, how many tiles it holds and the worst of what they say: `● 1 bad` for a tile that is an error or has a status or usage cell in the red, `● 2 warn` for the amber ones — a partial answer, a call waiting for you — `ok` when the tiles that grade themselves are fine, and `N loading` while some have not answered yet. Folding hides the panels and never a problem, at any width: a header that is short of room gives up the count first and then the end of its name, cut with an ellipsis, and keeps what the tiles say. `ok` is said only when a tile said so, since tiles that grade nothing say nothing. A page of key/value pairs has no status column and is read by the words its values begin with, the ones a status cell is coloured by: a docker daemon or a cluster that `did not answer`, `was never contacted` or `answered, and refused` is a bad tile, and a part it `could not be read` is a warning; a value that is a fact, even a good word like `up 3 days`, says nothing.

`width:`, `height:` and `refresh:` are how wide, how tall and how often the tiles of the section are, in place of the screen's ([below](#width-height-and-pace-one-rule-at-three-levels)). A name is a heading a person reads — up to 64 characters, no control characters — so `databases`, `prod: orders` and `pg · staging` all are. A file can state one that is not, and it is still drawn: quoted, with what it holds written out, on its header and in the notes that name it, so that a line break in it cannot make the header two lines.

What a file states about sections that does nothing is named, and never stops the file being read. `rta config check` and `rta doctor` report three things: a `sections:` entry that no tile of that screen is in, listed with the names there are so that a typo is one line from its fix; a name that cannot be a heading; and a `width:`, `height:` or `refresh:` outside what it takes, read as left out, on a section, a tile and the screen alike. The `pg · staging` a profile gives its panels is a name whenever that profile holds several connections, whichever environment is on, so stating how it starts is never reported as nothing. A section's `width:` or `height:` that is not a number, or a `sections:` that is not a mapping of names, does not load: the refusal names the file, the line, the key and what it takes.

From a shell, `--section` writes the entry into a section, and `rta dashboard sections` lists what is drawn and stated:

```bash
rta dashboard add pg.overview --profile staging/orders --section databases
rta dashboard add sys.overview --section host      # the automatic sys tile, now in a section: the entry takes its place
rta dashboard list                                 # a Section column says which section each tile is in
rta dashboard sections                             # every section of every view: tiles, how it starts, width, height, refresh, the file
```

A section is a fact about the entry, so `--section` is how a tile changes section: adding the same tile again replaces the entry, and without `--section` it is in none, as it is without its `--width`. Moving a tile with `[` `]` does not change its section, and at the edge of its section says so. `rta dashboard sections` counts what bare `rta` would draw now, so a group named after the environment, `pg · staging`, is counted while staging is switched on, and a section a view states that no tile is in is listed with none. A name that is not plain, in either table, is quoted the way it is on the header, so that it is one cell and never a row of its own in `csv`. In the TUI the header takes the arrows, `enter`, `space`, `z` and a click to fold it, `Z` folds every section, and `[` `]` on it move the section ([the TUI](./20-tui.md) has them all); a fold is for the session and is never written, so `collapsed:` is how a section starts and the only way to say it.

## Views: a screen per environment

A person who works against three environments wants three screens: staging's cluster and database, production's tiles and nothing else, and one that is about every database there is. A profile is a set of connections and not an arrangement, so the arrangement is named on its own, as a **view**, and a profile says which ones belong to it:

```yaml
dashboard:
  width: 6
  views:
    ops:
      width: 4
      tiles:
      - id: kube.overview
        profile: prod
      - id: pg.overview
        profile: prod
    databases:
      add:
      - id: pg.overview
        profile: prod
      - id: pg.overview
        profile: staging
      - id: pg.overview
        profile: analytics
profiles:
  prod:
    dashboard: [ops, databases]   # a name, or a list of names; the first is drawn on arrival
    plugins:
      pg: {}
```

Switching to `prod` draws `ops`, the first it lists, and `V` walks `ops` and `databases` and no others while `prod` is switched on, with `v` walking them the other way round. A profile that lists nothing, and no environment at all, draws the `dashboard:` block as it always has, and `V` walks every screen there is, so one environment's screens are never paged through on the way to another's. `default` is a legal entry in the list and is the `dashboard:` block. A view is a whole arrangement of the same six keys the block has — `tiles`, `add`, `hidden`, `order`, `columns`, `sections` — and it **replaces** the block while it is drawn rather than laying over it, so what a screen shows is the fields of one place and not an answer to which of two lists wins. Its tiles pin themselves to whichever profiles they name, which is what makes the `databases` view possible: it belongs to no environment, shows every database of every one, and any profile can list it. A profile that names a view nothing states draws the default and `rta doctor` says so, since a screen that fails to draw over a name is a worse outage than the usual one.

From a shell, `--view` names the view on every `rta dashboard` command, and without it a command acts on the view that is drawn now, so what you add, hide or list is what you are looking at:

```bash
rta dashboard add kube.overview --profile prod --view ops    # creates the view when it is new
rta dashboard views                                          # every view, which profiles link it, and the one drawn now
rta dashboard list --view databases
rta profile set prod --dashboard ops --dashboard databases   # the screens prod links, the first drawn on arrival; `--dashboard none` goes back
```

`--view default` names the `dashboard:` block when that is the one you mean. In the TUI the header says `view ops` when it is not the default, `H`, `+` and `[` `]` write to the view that is drawn, `V` draws the next screen the switched-on environment links — the default, then each view by name, when it links none — and `v` draws the one before, going round at either end, for this session only and nothing written. Without `--view`, a command acts on the screen the environment draws first, since the command line cannot see which one a TUI session is on; the receipt says so when the environment links several. A view another environment links is still one you can name with `--view` and edit, and still one `rta profile set --dashboard` can link. A selection on a section's header stays on it when the next screen has that section and goes to the screen's first header or tile when it does not, never to the search line, so `V` pressed again draws the one after. A switch (`u`) undoes it, since switching is asking for that environment's screen. Each view is a unit of its own when the configuration is [split across files](./22-your-config-file.md#split-it-across-files): a drop-in can hold one view and nothing else.

### A screen written in the profile

When only one environment shows a screen there is nothing to name. Write the arrangement under the profile's `dashboard:` instead of a view's name, with the same keys a view has:

```yaml
# ~/.config/rta/config.d/20-staging.yaml
profiles:
  staging:
    plugins:
      pg: {}
    dashboard:
      add:
      - id: kube.metrics.node
      - id: cnpg.storage
        profile: staging/orders
      hidden:
      - kube.metrics.node@prod/auth
      order:
      - note.list
      - sys.overview
```

Switching to `staging` draws that screen, and every other environment, and none, draws what it did. The block is a view named after its profile and nothing more, so each thing above works on it as it does on a view under `views:`: `rta dashboard views` lists it as `staging`, selected by `staging (its own)`, in the file that holds the entry; `--view staging` names it; `V` cycles to it; and with `staging` switched on it is the screen a bare `rta dashboard list` shows. Another profile can select it by name (`dashboard: staging`) as it can any view.

`rta dashboard add`, `hide`, `unhide` and `rm` write into the entry, in whichever file holds it, and never put a `views: staging:` beside it. With `staging` switched on, a command without `--view` acts on its screen, so what you add is what you are looking at, and the answer names the file it wrote. What a write rewrites is the profile's entry as a whole, as `rta profile set` does: the comments inside and around the block stay where they were, blank lines and flow style in it are written in block form, and a key rta does not read is dropped — `rta config check` names those first. A configuration that is all drop-ins stays that way: a write into one does not make the config file beside them.

A profile is one thing that lives in one file, and so is its screen: it is stated in the file that holds the entry, moves with it, and an entry stated in two files is refused as any profile is. That puts the environment's connections beside its screen in the drop-in, which is what a drop-in per environment looks like anyway.

A name has one statement. When `dashboard: views:` also has a view called `staging`, the name keeps meaning that view, the block in the profile is not drawn and cannot be addressed, and the `dashboard:` block draws while `staging` is switched on, rather than either of the two being chosen for you. `default` names the `dashboard:` block, so a profile called `default` cannot have its own screen addressed either. Both are reported where every profile problem is: `rta profile list` shows the profile as `warn` with the reason, `rta profile show` and `rta doctor` say it, and `rta config check` names the file and the line of the `dashboard:` key, which it reads across every file because the view and the profile are as likely to be in two. The profile is still one you can switch on, since nothing about its connections is in question, and it draws the `dashboard:` block until you keep one of the two.

`rta config check` holds the block to the keys a view has, so a `tile:` for `tiles:` inside a profile's entry is named with the line it is on, as it is under `views:`. A `dashboard:` that is none of a name, a list of names and a block — a number, a list of something else — is refused when the file is read, naming the file, the profile and the line, and what it takes.

A config file rta does not honour — the `./.rta.yaml` it falls back to when there is no config directory — arranges no screen, a profile's own included. Its blocks are read and listed, the automatic dashboard is what draws, and the dashboard commands refuse to write there, as they do for the `dashboard:` block and every view.

## Colours

`t` opens the theme editor, a form with one box per colour and a preview above it, and saves what you changed into the config file — into the top-level block, or into one profile's ([a palette per environment](#a-palette-per-environment)). The same block can be written by hand, and the CLI draws with it too:

```yaml
theme:
  primary: "#D97757"     # identity: keys, titles, the selection
  good: "#3ED598"        # a status that is fine
```

There are ten colours to name — `primary`, `accent`, `muted`, `faint`, `label`, `good`, `warn`, `bad`, `inverse` and `ink` — each as `#rrggbb`, and one you leave out keeps the built-in. `faint` is structure (borders, chart fills), `muted` is secondary text, `label` is the name of a pane, `good`, `warn` and `bad` are the three status colours, and `inverse` and `ink` are the text on a coloured badge. Anything else, or a colour that is not that form, is left out and costs only its own line: the rest apply and `rta doctor` reports the entry. A name that is nearly a slot (`Label`, `primry`) is reported with the one it nearly is. A colour is quoted in the file because a `#` starts a YAML comment: `label: #C792EA` states nothing, and is reported as a colour with no value, with the quoted form to write. `rta config schema` describes the block to an editor, so a name or colour that will not apply is marked as you type it.

### A palette per environment

An environment can have colours of its own, so that switching to it changes what the screen looks like the way it changes the dashboard. A profile takes the same `theme:` block, and its `color:` is its primary colour:

```yaml
theme:
  accent: "#D77015"
profiles:
  shop-prod:
    color: "#FF6B7A"
    theme:
      label: "#C792EA"
    plugins:
      pg:
        set: { host: db.internal }
```

While `shop-prod` is in force what is drawn comes from these, each over the one before: the built-in palette, the top-level `theme:`, the profile's `color:` as its `primary` (titles, keys, headings and the selection), and the profile's own `theme:`, which wins over its colour when it states a `primary` of its own. With nothing in force, or with `--no-profile`, the top-level `theme:` alone applies, as it always did. The colour that marks an environment in its badge is therefore also the colour of what you read in it, which is the point: it is on every result, not only the line above one.

From a script, or without opening the file:

```bash
rta profile set shop-prod --plugin pg --set host=db.internal --color '#FF6B7A' --theme label='#C792EA'   # the entry above, written from flags
rta profile set shop-prod --untheme                                                                      # take its theme: block off
```

`--theme` states the block whole, as `--set` does, so a slot you leave out is removed and the receipt says which. A slot the palette does not have, a colour that is not `#rrggbb` and a status colour (below) are refused before anything is written, and the flag completes the five slots a profile can state. `rta profile show` prints the palette that stands beside the profile's colour and `rta profile list` counts it in a `Theme` column, as the profiles pane in the TUI does: what is counted and named is what is applied, so an entry the palette leaves out is not in either. It is a warning on the profile, which is still listed as usable and still switched to, in `rta profile list`, `show` and `rta doctor`, and `rta config check` names its line (`profiles.shop-prod.theme.label`). It costs only itself: the rest of the block applies, and the colour beneath it, yours and not the built-in, stands in its place.

**Which environment.** The one a call names with `--profile`, else the one `rta use` switched on, and none for a call that leaves it with `--no-profile` or that goes to a connection typed on its own command line, which is nobody's environment. `shop-prod/analytics` is shop-prod's colours: an instance is one of the places a profile reaches, not a second environment with a screen of its own. A command with no `--profile` of its own takes the one that is switched on. `rta use shop-prod` draws its own receipt in the colours it switched to, and `rta use --off` in yours.

**The editor.** `t` opens on a choice of whose palette you are editing: `global`, the top-level block, and each profile you have, with the environment that is switched on already chosen — or straight on the top-level block when you have no profile, since a choice of one is not a choice. A profile's form has the five boxes for the colours an environment can state — `primary`, `accent`, `label`, `muted` and `faint`; the status colours are not offered, since they can only be set in your own config file — and the preview above it shows the whole palette that place would produce: a box left empty shows what lies beneath it (your own colour, or the profile's colour as its primary) and not the built-in, and a colour that would be left out — not a colour, one too dim to be seen, or a primary that is a status colour — is a cross. The box refuses it too, with the reason (`would be left out: it is the same colour as bad…`), so it is not saved as a line the palette would not use. The boxes are seeded from what that block states and nothing else, so your own colours are never copied into a profile where they would stop following the edits you make to them. Saving writes into the place picked and only there: a profile's other keys are carried as they were, a profile that lives in a drop-in is written in the drop-in, an untouched form writes nothing, and one colour that cannot be used costs its own line in the report while the rest apply. A save that still leaves something out — the file changed under the form — says how many and ends on `audit.doctor`, which prints which and why, and a status colour that was written into a profile by hand has no box and is dropped by the save, which says so. Saving a profile that is not switched on paints nothing, and the footer says it is drawn while it is.

**In the TUI** the screen is drawn for the environment that is switched on, and follows it: `u` on the profiles pane, an `rta use` from another terminal and a deadline lapsing repaint it, and so does an edit of the profile in force — in the profile form, or in the file, where the next refresh reads it. The whole screen is one environment's, so a tile pinned to another profile does not change it: that tile keeps its profile's name painted in that profile's colour on its panel, and nothing else of the screen moves. The spinner and a result already on screen are drawn again with the new colours, so nothing is left in those of the environment you came from. A form is drawn in the same palette (the rail, the selected row and the cursor in the identity colour, a title in the pane-name colour, an error in the failure colour), so the place where something is typed is not the one place the environment's colour stops.

**An environment cannot make one thing read as another.** The status colours — `good`, `warn` and `bad`, and `inverse` and `ink`, the text of the two badges that say the last two — are what you read a screen by, and they are yours: only your own config file sets them. A profile's `theme:` may not state them at all, nor may the top-level `theme:` of a config file you did not name (a `./.rta.yaml` in a repository you cloned). An environment can then neither make a failure read as success, nor nudge a status a digit from another, nor put the badge's text a digit from its own background, and no threshold has to guess how near is too near: the entry is left out, the colour beneath it stands, and `rta doctor` names the line and how to set it yourself (`rta config set theme.good '#3ED598'`).

The colours that are text — `primary`, `accent`, `muted`, `faint` and `label` — may be stated, and must stay visible on either kind of terminal: one under 1.5:1 contrast against pure black, or against pure white, is left out, and the line says the ratio it measured. That is a floor against being invisible, and not a standard for legibility — the built-in colours are at 3.4:1 or better on white and 6.2:1 on black, and a near-black `muted` would draw a screen nobody could read on a dark terminal — so what you chose to see passes it, `#dd3333`, `#C792EA` and `#FFB000` among them. A profile's `color:` is held to it as the screen's primary: one too dim is skipped there, and its badge keeps it, since the badge picks its own text. The primary, stated or taken from the profile's colour, is also left out when it is exactly a status colour that stands, because the selection would read as ok, warn or failed. Your own top-level `theme:`, in a file you named, is held to none of this: stating `good` and `bad` alike, or a primary you cannot see, is a choice about your own screen.

**Whose file.** A profile in a config file you did not name is not honoured at all, and what it says about colour is not either: a repository cannot repaint an environment's screen any more than it can point one at a database. The top-level `theme:` of such a file still applies, as it did before environments had colours of their own, less the status colours it cannot state and held to the floor above. The same holds for a drop-in: a profile's `theme:` lives in the file that states the profile.

## Related

- [The TUI](./20-tui.md) — the keys that do the same from the dashboard
- [Your config file](./22-your-config-file.md) — where the file is and `rta config set`, which writes a colour or a column count for you

## Next

[Config examples](./26-config-examples.md) — a cluster and its databases, the services reached over the network, roles and a dashboard, one need at a time.
