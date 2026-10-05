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

**One entry, one panel per connection.** A profile can hold several connections to the same plugin — `cnpg/gitea`, `cnpg/keycloak`, three more — and a tile whose profile names no instance becomes one panel per connection that profile holds for its plugin, each named on its panel. `{id: cnpg.overview, profile: ohmlab}` is every cnpg database ohmlab knows, and one added to the profile next month gets its panel on its own, the same rule the automatic set follows for a plugin installed next month. A tile that follows the switch expands the same way, into whatever environment is on: under ohmlab it is ohmlab's databases, under mirai-prod that environment's own. Name the instance, `profile: ohmlab/gitea`, for one panel. This is the one place a bare profile over several connections is not refused with "your call", as `--profile ohmlab` is on a single command: a dashboard is not a choice, showing every one is the glance, and no wrong pick is possible.

The same from a script, or without opening the file:

```bash
rta dashboard add kube.overview --profile prod
rta dashboard add kube.overview --profile staging
rta dashboard add cnpg.overview --profile ohmlab               # one panel per cnpg connection ohmlab holds
rta dashboard add pg.overview --profile staging/analytics --span 2
rta dashboard add cert.expiry --set host=example.com
rta dashboard list                                             # every panel bare rta would draw, where each came from, and what is hidden
rta dashboard hide cnpg.overview --profile ohmlab/keycloak     # that one panel; its siblings stay
rta dashboard unhide cnpg.overview --profile ohmlab/keycloak
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
- **The whole block is ignored unless the config is one you named** — your user config directory, or `RTA_CONFIG`. rta falls back to `./.rta.yaml` when there is no user config directory (ordinary under `env -i`, in a container, in CI), and a cloned repository does not get to arrange your screen: `{id: http.get, with: {url: …}}` there would be a beacon that starts the moment you open the TUI in that directory. `hidden:` is the same hazard pointed the other way — it can take the agent tile off the screen, and that tile is where you notice a parked consent request before its clock runs out.

## Colours

`t` opens the theme editor, a form with one box per colour and a preview beside it, and saves what you changed into the config file. The same block can be written by hand, and the CLI draws with it too:

```yaml
theme:
  primary: "#D97757"     # identity: keys, titles, the selection
  good: "#3ED598"        # a status that is fine
```

There are ten colours to name — `primary`, `accent`, `muted`, `faint`, `label`, `good`, `warn`, `bad`, `inverse` and `ink` — each as `#rrggbb`, and one you leave out keeps the built-in. `faint` is structure (borders, chart fills), `muted` is secondary text, `label` is the name of a pane, `good`, `warn` and `bad` are the three status colours, and `inverse` and `ink` are the text on a coloured badge. Anything else, or a colour that is not that form, is left out and costs only its own line: the rest apply and `rta doctor` reports the entry. `rta config schema` describes the block to an editor, so a name or colour that will not apply is marked as you type it.

## Related

- [The TUI](./20-tui.md) — the keys that do the same from the dashboard
- [Your config file](./22-your-config-file.md) — where the file is and `rta config set`, which writes a colour or a column count for you

## Next

[Seeing the shape of things](./30-trees.md) — mapping a directory, a bucket, a Vault mount or an etcd keyspace in one call.
