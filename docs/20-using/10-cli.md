# The CLI

Every capability is a command, and every command follows the same rules. Once you know the rules, you know the whole surface — including the parts added by plugins you install later.

```
rta <plugin> <capability> [arguments] [--flags]
```

```bash
rta sys cpu --cores
rta net dns github.com --type MX
rta fs usage ~/Downloads --depth 2
```

## Output formats

`--output` (or `-o`) is on every command:

| Format | For |
| --- | --- |
| `pretty` | A person at a terminal. Tables, colour, charts. **Default** |
| `json` | Scripts and `jq` |
| `yaml` | Scripts that prefer it, and diffing |
| `csv` | Spreadsheets and appending to a file |
| `md` | Dropping into a report or an issue |

```bash
rta sys overview -o json | jq '.rows'
rta audit web example.com -o md >> security-review.md
```

`RTA_OUTPUT` sets the default, so a machine that is always scripting can say it once:

```bash
export RTA_OUTPUT=json
```

**Say what you want in a script.** `pretty` is a rendering choice made for humans, and it is the one format whose shape is allowed to change.

### The shape of a result

Every capability returns the same envelope — columns and rows, sometimes composed into sections. That is why one `--output` flag works everywhere, and why a plugin cannot invent a format your tooling has not seen.

```bash
rta net dns github.com -o json
```

```json
{
  "columns": [{ "name": "Type" }, { "name": "Value" }],
  "rows": [["A", "140.82.121.4"]]
}
```

A column may carry a `kind` — a semantic hint the renderers style by, never styling itself. Two of them are graded rather than merely aligned: a `status` column colours its own vocabulary (`ok`, `warn`, `expired`, `denied`…), and a `usage` column is a percentage of something with a ceiling, drawn green below 80%, amber from 80 and red from 90. That is what makes a full volume in `rta kube pvc usage`, a throttled container in `kube metrics pod` and a namespace out of quota findable by glance rather than by reading every row.

Not every percentage is one. `fs usage`'s Share is a directory's proportion of a tree and 95% is the answer you ran it for; `sys ps`'s CPU% passes 100 on a second core; `kube metrics pressure` reports stall time, where a node is already in trouble far below 80. Those stay `percent` — ungraded, and deliberately so.

Colour is never the only signal. `sys disk` states the same band as a word in a `Status` column beside its `Use%`, which is the half that survives `--no-color`, a pipe and `-o json`.

**A machine-readable format means machine consumption, and rta treats it that way.** With `-o json|yaml|csv|md`, stdout carries the view and nothing else — errors go to stderr, also in the format you asked for, and the startup notice about untrusted artifacts is suppressed entirely. So the output on your screen is the output a parser accepts, which is where copy-and-paste gets it from:

```bash
# Approve every artifact rta found and refused to run.
rta plugin trust -o json | jq -r '.rows[][0]' | xargs -rn1 rta plugin trust

# Every profile, and what each one covers.
rta profile list -o json | jq -r '.rows[] | "\(.[0])\t\(.[1])"'

# Ship the record onward from where you left off.
rta agent log --after "$cursor" -o json | jq -c '.rows[]'
```

Exit codes make the loop safe to write, and they are the next thing worth knowing.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | rta refused, or the operation failed — a structured error with a code and a hint |
| `2` | Something unexpected went wrong |
| `3` | Confirmation required, and `--yes` was not given |

Code `3` is the one worth handling in scripts. It means the command was destructive and nobody confirmed — not that anything failed.

```bash
rta note rm 4 || case $? in
  3) echo "needs --yes" ;;
  1) echo "refused or failed" ;;
esac
```

Errors carry a stable code and an actionable hint, in whatever format you asked for:

```bash
rta kv get missing-key -o json
```

```json
{ "code": "kv.notfound", "message": "no key \"missing-key\"", "hint": "…" }
```

The code is stable across versions; the message is not. Match on the code.

## `--dry-run`

Reports what would happen without doing it, on any capability that changes something:

```bash
rta net hosts add 10.0.0.5 db.local --dry-run
rta note rm 4 --dry-run
```

This is not only a convenience for you — it is what rta shows an operator on a [parked agent call](../30-boundary/40-audit-trail.md#parked-calls), which turns "may this agent call `note.rm`" into "may it remove **this note**", and it is what [the TUI](./20-tui.md#running-something) shows you before a destructive run, for the same reason.

## `--yes`

Skips confirmation prompts. Destructive capabilities ask before acting when a human is present; `--yes` (or `-y`) is how a script says it means it.

```bash
rta note rm 4 --yes
```

## `--help`

Every command's `--help` describes what it does, the arguments it takes, and the flags it accepts.

```bash
rta cert expiry --help
```

```
  Arguments:
    targets  hosts to check (host[:port])

  USAGE

    rta cert expiry <targets> [--flags]
```

The arguments are read from the same declaration as the flags below them, so a capability cannot take one without documenting it — `<targets>` alone would never tell you it accepts a port.

## `rta explain`

The authoritative reference for anything, and the one that goes deeper than `--help`: types, defaults, which config key fills an input, and whether an MCP caller may supply it at all.

```bash
rta explain              # every capability
rta explain kv.get       # one, in full
```

```
id           sys.cpu
summary      Show CPU model, core count and current usage
safety       read
idempotent   true
cli          rta sys cpu [--cores <bool>]
mcp-tool     sys_cpu
input:cores  bool — per-core usage as a bar chart
```

This is generated from the same declaration the CLI parser, the TUI form and the MCP schema are built from. It cannot drift, which is more than most documentation can say — including this page.

## Completion

rta's completion goes further than subcommand names. A capability that declares `Options` completes to its allowed values; one that declares `Suggest` completes from **what actually exists on your machine** — your tags, your keys, your hosts file — and only on human surfaces. `Path` inputs complete filesystem paths.

See [Installation](../10-getting-started/10-installation.md#shell-completion) to turn it on.

## Piping in

A few capabilities read a pipe when the argument naming their input is left out: `debug ansi` the text it explains, `keys restore` the seed words, `codec jwt` the token and `codec jwk` the key. For the last three that is the point — a pipe keeps a bearer token or a seed phrase out of `ps` and out of your shell history, where an argument is readable by every user on the machine for as long as the call runs:

```bash
kubectl get secret app -o jsonpath='{.data.token}' | base64 -d | rta codec jwt
```

They read the pipe only on the CLI and only when stdin is not a terminal, so the pipe read never waits on a keyboard; `keys restore`, given no `--words` and no pipe, asks for the words at a masked prompt instead. Each reads up to a bound — a pipe holding more is refused rather than cut short. Nothing else infers an input from a pipe. What every `Path` input accepts is `/dev/stdin`:

```bash
echo -n hello | rta fs hash /dev/stdin
kubectl get secret db -o jsonpath='{.data.password}' | base64 -d | rta kv set db-password --file /dev/stdin
```

The second line is the one that matters: it is how a credential reaches the store without ever being an argument in `ps` or in your shell history.

## Scripting notes

- **Be explicit about format.** `-o json` or `RTA_OUTPUT=json`.
- **Match error codes, not messages.**
- **`3` is not a failure.** It is a question you did not answer.
- **Bare `rta` in a pipe prints help** rather than opening the TUI, so a script never hangs on an invisible interface.
- **`--no-color` is honoured**, and colour is already suppressed when stdout is not a terminal.
- **Pretty output takes `COLUMNS` when it is set**, over the terminal's own width and through a pipe too: `COLUMNS=100 rta doctor | less`. Without it a pipe gets every table at its natural width, which does not depend on whose window ran the command. With it a pipe is shaped as a terminal that wide would be, so a value longer than the width — a token, a hash — is broken across lines there too; a script reads values from `-o json`, never from the pretty layout.
- **Setup itself is scriptable.** `rta profile set` and `rta policy` state an environment and a ceiling from flags, and `rta dashboard add` puts a tile on the TUI's landing screen, all idempotent, so provisioning does not have to fall back to writing YAML by hand — see [Profiles](./40-profiles.md#writing-one-from-a-script) and [The TUI](./20-tui.md#stating-the-dashboard-yourself).
- **Never pass a credential on a command line.** Store it (`rta kv set <entry> --file <path>`, or `--file /dev/stdin` from a pipe) and reference it: `--secret password=kv:<entry>`. A value in argv is in `ps`, in your shell history, and in most CI logs. `rta profile set` refuses one rather than writing it.

## Next

- [The TUI](./20-tui.md) — the same capabilities, interactively
- [Recipes](../90-recipes/01-readme.md) — worked examples
