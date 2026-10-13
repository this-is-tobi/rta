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
  columns: 3
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
    span: 2
```

`profile:` pins a tile to one connection — a profile, or `name/instance` for one of several connections to the same plugin — and the tile is about that connection whatever `rta use` switched on, with the name on its panel. That is what lets one capability sit on the dashboard twice, once per cluster. Without it a tile follows the switched-on environment: switch to staging and the pg tile is about staging. `with:` fills the capability's inputs. `span:` widens a tile past what its own declared width works out to — for the one you actually read.

**One entry, one panel per connection.** A profile can hold several connections to the same plugin — `pg/orders`, `pg/analytics`, three more — and a tile whose profile names no instance becomes one panel per connection that profile holds for its plugin, each named on its panel. `{id: pg.overview, profile: staging}` is every Postgres database staging knows, and one added to the profile next month gets its panel on its own, the same rule the automatic set follows for a plugin installed next month. A tile that follows the switch expands the same way, into whatever environment is on: under staging it is staging's databases, under prod that environment's own. Name the instance, `profile: staging/analytics`, for one panel. This is the one place a bare profile over several connections is not refused with "your call", as `--profile staging` is on a single command: a dashboard is not a choice, showing every one is the glance, and no wrong pick is possible.

The same from a script, or without opening the file:

```bash
rta dashboard add kube.overview --profile prod
rta dashboard add kube.overview --profile staging
rta dashboard add pg.overview --profile staging                # one panel per pg connection staging holds
rta dashboard add pg.overview --profile staging/analytics --span 2
rta dashboard add cert.expiry --set host=example.com
rta dashboard list                                             # every panel bare rta would draw, where each came from, and what is hidden
rta dashboard hide pg.overview --profile staging/orders        # that one panel; its siblings stay
rta dashboard unhide pg.overview --profile staging/orders
rta dashboard rm kube.overview --profile staging
```

`add` refuses what the file would have quietly got wrong: a capability that is not a read, an input it does not declare, a credential under `--set`, a `--set` value of a type the input cannot hold (`core.dashboard.set.type`) or outside its options or range (`core.input.option`, `core.input.range` — the codes a run would refuse it with, on every refresh), a required input nothing fills, a profile that does not cover the plugin. Adding the same tile again replaces it, so the command is safe in a script that runs on every boot. In the TUI, `H` on an added tile withdraws its entry rather than hiding the capability — hiding by name would take both kube tiles down — and the footer says the command that puts it back; `H` on one panel of an entry that expanded hides that connection's panel by its key and leaves the rest, and `unhide` is the way back. `hide` and `unhide` are what `H` and the inventory pane do, from a script. Adding a capability the automatic dashboard already shows makes the entry that tile: `rta dashboard add sys.overview --span 2` widens it rather than doubling it, and the bare form is refused, with the way back when the tile is hidden. `[` and `]` move an entry that expanded as one, its panels together, because the file can place the entry and nothing finer.

**Or state it exactly.** `tiles:` replaces the automatic set outright — `hidden:` and `order:` are not consulted, because the list is already both, except for a `hidden:` key naming one panel of an entry that expanded, which is a panel and not an entry the list could drop. Its entries take the same `profile:`, `with:` and `span:`, and `add:` entries follow the list:

```yaml
dashboard:
  tiles:
  - id: kube.overview
    profile: prod
  - id: note.list
    span: 2
```

Worth knowing what a tile costs before you add one: it refreshes on a timer for as long as the TUI is open, so a cluster-wide `kube.overview` tile is a round of `kubectl` calls at every refresh, for every hour the terminal stays open. `rta explain kube.overview` prints what a capability actually reads, which is not always only what its name suggests, and its `dashboard` row says how often a tile of it re-runs.

A capability can set its own pace. The dashboard's timer runs every few seconds, and that is what a tile gets unless its capability declared a longer interval — `eol.watch`, `eol.check` and `eol.products` re-run every two hours, `pkg.overview`, `pkg.outdated` and `pkg.os` every hour — because their answers move by the day and a run costs a request per product or a dozen subprocesses. A plugin's capability declares one the same way: `kube.overview` re-runs every minute, since a run is five cluster-wide lists at once and nothing it reports moves faster than the cluster's own controllers decide it. So `rta dashboard add eol.watch` is the version watchlist on your landing screen at a pace endoflife.date would not notice. Switching environments re-runs every tile that follows the switch, since its inputs just changed, and leaves a pinned tile inside its pace, since its did not.

Two things the block will not do, whatever you write in it:

- **A tile that is not `Read` is dropped.** Otherwise `{id: kv.rm, with: {key: old-token}}` would delete that key on startup and keep deleting it — on a timer, with no form and no confirmation, since the destructive gate lives on the CLI and the browse path and a tile goes through neither.
- **The whole block is ignored unless the config is one you named** — your user config directory, or `RTA_CONFIG`. rta falls back to `./.rta.yaml` when there is no user config directory (ordinary under `env -i`, in a container, in CI), and a cloned repository does not get to arrange your screen: `{id: http.get, with: {url: …}}` there would be a beacon that starts the moment you open the TUI in that directory. `hidden:` is the same hazard pointed the other way — it can take the agent tile off the screen, and that tile is where you notice a parked consent request before its clock runs out. `rta config check` says the block is ignored, `rta dashboard views` says none of the views it lists is drawn, and `V` has no views to cycle through.

## Views: a screen per environment

A person who works against three environments wants three screens: staging's cluster and database, production's tiles and nothing else, and one that is about every database there is. A profile is a set of connections and not an arrangement, so the arrangement is named on its own, as a **view**, and a profile says which one it shows:

```yaml
dashboard:
  columns: 2
  views:
    ops:
      columns: 3
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
    dashboard: ops
    plugins:
      pg: {}
```

Switching to `prod` draws `ops`; any other environment, and no environment, draws the `dashboard:` block as it always has. A view is a whole arrangement of the same five keys the block has — `tiles`, `add`, `hidden`, `order`, `columns` — and it **replaces** the block while it is drawn rather than laying over it, so what a screen shows is the fields of one place and not an answer to which of two lists wins. Its tiles pin themselves to whichever profiles they name, which is what makes the `databases` view possible: it belongs to no environment, shows every database of every one, and any profile can select it. A profile that names a view nothing states draws the default and `rta doctor` says so, since a screen that fails to draw over a name is a worse outage than the usual one.

From a shell, `--view` names the view on every `rta dashboard` command, and without it a command acts on the view that is drawn now, so what you add, hide or list is what you are looking at:

```bash
rta dashboard add kube.overview --profile prod --view ops    # creates the view when it is new
rta dashboard views                                          # every view, who selects it, and the one drawn now
rta dashboard list --view databases
rta profile set prod --dashboard ops                         # the screen prod shows; `--dashboard none` goes back
```

`--view default` names the `dashboard:` block when that is the one you mean. In the TUI the header says `view ops` when it is not the default, `H`, `+` and `[` `]` write to the view that is drawn, and `V` draws the next one — the default, then each view by name — for this session only, which is how you look at the databases screen without switching environment. A switch (`u`) undoes it, since switching is asking for that environment's screen. Each view is a unit of its own when the configuration is [split across files](./22-your-config-file.md#split-it-across-files): a drop-in can hold one view and nothing else.

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

`rta config check` holds the block to the keys a view has, so a `tile:` for `tiles:` inside a profile's entry is named with the line it is on, as it is under `views:`. A `dashboard:` that is neither a name nor a block — a list, a number — is refused when the file is read, naming the file, the profile and the line, and what it takes.

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
rta profile set shop-prod --color '#FF6B7A' --theme label='#C792EA'   # the whole theme: block of the profile
rta profile set shop-prod --untheme                                   # take it off
```

`--theme` states the block whole, as `--set` does, so a slot you leave out is removed and the receipt says which. A slot the palette does not have, a colour that is not `#rrggbb` and a status colour (below) are refused before anything is written, and the flag completes the five slots a profile can state. `rta profile show` prints the palette that stands beside the profile's colour and `rta profile list` counts it in a `Theme` column, as the profiles pane in the TUI does: what is counted and named is what is applied, so an entry the palette leaves out is not in either. It is a warning on the profile, which is still listed as usable and still switched to, in `rta profile list`, `show` and `rta doctor`, and `rta config check` names its line (`profiles.shop-prod.theme.label`). It costs only itself: the rest of the block applies, and the colour beneath it, yours and not the built-in, stands in its place.

**Which environment.** The one a call names with `--profile`, else the one `rta use` switched on, and none for a call that leaves it with `--no-profile` or that goes to a connection typed on its own command line, which is nobody's environment. `shop-prod/analytics` is shop-prod's colours: an instance is one of the places a profile reaches, not a second environment with a screen of its own. A command with no `--profile` of its own takes the one that is switched on. `rta use shop-prod` draws its own receipt in the colours it switched to, and `rta use --off` in yours.

**The editor.** `t` opens on a choice of whose palette you are editing: `global`, the top-level block, and each profile you have, with the environment that is switched on already chosen — or straight on the top-level block when you have no profile, since a choice of one is not a choice. A profile's form has the five boxes for the colours an environment can state — `primary`, `accent`, `label`, `muted` and `faint`; the status colours are not offered, since they can only be set in your own config file — and the preview above it shows the whole palette that place would produce: a box left empty shows what lies beneath it (your own colour, or the profile's colour as its primary) and not the built-in, and a colour that would be left out — not a colour, one too dim to be seen, or a primary that is a status colour — is a cross. The box refuses it too, with the reason (`would be left out: it is the same colour as bad…`), so it is not saved as a line the palette would not use. The boxes are seeded from what that block states and nothing else, so your own colours are never copied into a profile where they would stop following the edits you make to them. Saving writes into the place picked and only there: a profile's other keys are carried as they were, a profile that lives in a drop-in is written in the drop-in, an untouched form writes nothing, and one colour that cannot be used costs its own line in the report while the rest apply. A save that still leaves something out — the file changed under the form — says how many and ends on `audit.doctor`, which prints which and why, and a status colour that was written into a profile by hand has no box and is dropped by the save, which says so. Saving a profile that is not switched on paints nothing, and the footer says it is drawn while it is.

**In the TUI** the screen is drawn for the environment that is switched on, and follows it: `u` on the profiles pane, an `rta use` from another terminal and a deadline lapsing repaint it, and so does an edit of the profile in force — in the profile form, or in the file, where the next refresh reads it. The whole screen is one environment's, so a tile pinned to another profile does not change it: that tile keeps its profile's name painted in that profile's colour on its panel, and nothing else of the screen moves. The spinner and a result already on screen are drawn again with the new colours, so nothing is left in those of the environment you came from.

**An environment cannot make one thing read as another.** The status colours — `good`, `warn` and `bad`, and `inverse` and `ink`, the text of the two badges that say the last two — are what you read a screen by, and they are yours: only your own config file sets them. A profile's `theme:` may not state them at all, nor may the top-level `theme:` of a config file you did not name (a `./.rta.yaml` in a repository you cloned). An environment can then neither make a failure read as success, nor nudge a status a digit from another, nor put the badge's text a digit from its own background, and no threshold has to guess how near is too near: the entry is left out, the colour beneath it stands, and `rta doctor` names the line and how to set it yourself (`rta config set theme.good '#3ED598'`).

The colours that are text — `primary`, `accent`, `muted`, `faint` and `label` — may be stated, and must stay visible on either kind of terminal: one under 1.5:1 contrast against pure black, or against pure white, is left out, and the line says the ratio it measured. That is a floor against being invisible, and not a standard for legibility — the built-in colours are at 3.4:1 or better on white and 6.2:1 on black, and a near-black `muted` would draw a screen nobody could read on a dark terminal — so what you chose to see passes it, `#dd3333`, `#C792EA` and `#FFB000` among them. A profile's `color:` is held to it as the screen's primary: one too dim is skipped there, and its badge keeps it, since the badge picks its own text. The primary, stated or taken from the profile's colour, is also left out when it is exactly a status colour that stands, because the selection would read as ok, warn or failed. Your own top-level `theme:`, in a file you named, is held to none of this: stating `good` and `bad` alike, or a primary you cannot see, is a choice about your own screen.

**Whose file.** A profile in a config file you did not name is not honoured at all, and what it says about colour is not either: a repository cannot repaint an environment's screen any more than it can point one at a database. The top-level `theme:` of such a file still applies, as it did before environments had colours of their own, less the status colours it cannot state and held to the floor above. The same holds for a drop-in: a profile's `theme:` lives in the file that states the profile.

## Related

- [The TUI](./20-tui.md) — the keys that do the same from the dashboard
- [Your config file](./22-your-config-file.md) — where the file is and `rta config set`, which writes a colour or a column count for you

## Next

[Seeing the shape of things](./30-trees.md) — mapping a directory, a bucket, a Vault mount or an etcd keyspace in one call.
