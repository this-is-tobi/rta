# rta-plugin-eol

An [rta](https://github.com/this-is-tobi/rule-them-all) plugin: end-of-life
and support-window checks against the public [endoflife.date](https://endoflife.date)
API.

No account, no key, no config section — install it and `rta eol check` works.

## Try it

    rta plugin dev                              # build and check what rta sees
    rta plugin dev -- eol check postgresql       # every release cycle, graded
    rta plugin dev -- eol check postgres 15      # one cycle; aliases resolve too

## Install

    go build -o ~/.local/bin/rta-plugin-eol .

Anything named `rta-plugin-*` on your $PATH is a plugin. The name after the
prefix is only a filename; the namespace comes from what the plugin declares,
so rta validates it and refuses a collision with an existing one.

## What you get for free

One declaration in `main.go` becomes:

- a CLI command — `rta eol check postgresql 15 --warn-days 30`
- a TUI form, with completion for any input that declares `Options` or `Suggest`
- an MCP tool for AI agents, gated by the capability's `Safety`
- `-o json|yaml|csv|md`, from the same `view.View` you returned

## Rules worth knowing

- **`Safety` is a claim about blast radius**, not a label. `Read` is exposed to
  agents by default; `Write` needs the operator's `--allow-write eol`;
  `Destructive` needs an explicit per-capability allowlist and a human-issued grant.
  Everything here is `Read`: it reveals nothing the caller did not already have
  the means to ask a public website for.
- **Return a `view.Error`, not a bare error**, when you can. The code is stable
  enough to branch on and the hint is what the person does next — see
  `fetchProduct` in `client.go` for a worked example, including the one that
  turns a 404 into "see https://endoflife.date" rather than a decode panic.
- **A word already in `sdktest.Vocabulary()` has to mean the same thing here
  that it means everywhere else.** A genuinely new operation is free to coin
  its own word — this plugin's one capability is `eol.check`, not forced into
  `get`/`status`/`inspect` — but `sdktest` still names it on every test run as
  a deliberate nudge to double check, not a one-time warning to silence.
- **Your process is confined on macOS.** It cannot read or write rta's own data
  directory, and cannot read the usual credential locations. `rta doctor` prints
  the exact set. This plugin needs none of that: one outbound HTTPS GET is the
  entire footprint.
- **Your stdin is /dev/null.** The protocol owns the real one. Ask for a secret
  with a `plugin.Secret` input instead of prompting.
