# The plugin inventory

[Using plugins](./10-plugins.md) is how a plugin arrives and what trusts it. This is the same state seen from inside the TUI, where a refusal would otherwise be silent.

## The plugin inventory

`p` opens what is installed, what each plugin puts on the dashboard, and — the part worth having a pane for — **any artifact rta found on `$PATH` and refused to run**. A trust gate's failure mode is silence: a plugin that is installed and doing nothing looks exactly like one that was never installed.

| Key | What it does |
| --- | --- |
| `p` | Open the inventory (and close it) |
| `space` | Show or hide its dashboard tile |
| `t` | Approve an artifact, or take an approval back |
| `a` | Choose which credential locations it may read |
| `c` | Configure it |
| `enter` | Its capabilities, in the search bar |

### Grouped by where the bytes came from

The pane bands its rows by provenance, because that is the fact that changes how every other fact on a row reads. "13 capabilities, one of them destructive" means one thing about code compiled into the binary you chose to run and something else about a file that appeared on your `$PATH`, and a list sorted by name buried the two or three you did not compile among a dozen you did.

| Band | What it means |
| --- | --- |
| **built in** | Compiled into the rta binary you are running, which is why these need no digest |
| **installed by rta** | rta placed these bytes from an index you attached; the row carries the version, the index and what the signature check found |
| **found on $PATH** | Binaries rta did not place and holds no record of |
| **not run** | Discovered and never launched, because nothing has approved them yet |

A stock install is entirely built in, so no bands are drawn at all — one band separates nothing.

**There is no "official" band, and that is a fact about rta rather than an omission.** The bands say how the bytes arrived, and the first-party index is only one place they can arrive from: a plugin installed from it is *installed by rta* like any other, with the index named on its row. rta attaches no index until you ask — `rta plugin index add official` attaches the first-party one, the only name it reserves — and a band named for an index would put rta's voice behind a source instead of behind the bytes. What rta genuinely knows is whether it placed the bytes itself, and that is what the bands say. Trust here binds to a digest, never to a name — which is also why an `rta.lock` record is matched to a row by digest: an entry naming this plugin and describing different bytes belongs to a half-finished upgrade, not to what you are running.

`t` is the decision made where the evidence is: the digest and the path are on the screen while you take it, which the command line shows you only afterwards. Neither direction takes effect on the process you are in — trust is read once, before anything is launched — so approving says it loads when rta restarts, and withdrawing says the plugin already running stays running until rta exits.

`a` is the permission after that one. Approving says these bytes may run; allowing says what they may read — a kubeconfig, an SSH directory, whatever the plugin declares it needs. The row shows both sides: what it has been allowed, and what it is still asking for.

**`a` opens a form where `t` is a keypress, and the difference is deliberate.** Approving is one yes/no about one thing already named on the row. Allowing is plural — a plugin can declare several locations, and a bare key would hand over every one of them from a cursor position. The form is also what makes taking access back expressible: the list you submit *is* the whole grant, so clearing a box withdraws that location and there is no second command to learn. A plugin you have not approved yet cannot be allowed anything — running at all is the decision that comes first, and the pane says so rather than opening a form that could not succeed.

## Untrusted plugins

If rta found an `rta-plugin-*` binary it has not been told to run, the TUI says so in a pane rather than a startup line. The line would be written to the primary buffer, and the TUI opens on the alternate one — so it would be covered before anyone could read it. The pane is the only place a person inside the TUI can learn a decision is pending.

See [Trust](./10-plugins.md#trust).

## Related

- [Using plugins](./10-plugins.md) — trust, indexes, installing and upgrading
- [The TUI](../20-using/20-tui.md) — the rest of the keys

## Next

[Writing a plugin](./20-writing-a-plugin.md) — the SDK, the conformance suite, and publishing to an index.
