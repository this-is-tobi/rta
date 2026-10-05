package app

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// The completion commands are cobra's, and so was their help: the recipe
// `rta completion zsh > "${fpath[1]}/_rta"`, which fails with "permission
// denied" wherever the first directory on fpath is one the person cannot write
// — kitty's shell integration puts its own there, owned by root. The
// installation page says to avoid exactly that, and the binary's own help was
// the place that taught it.
//
// So the three shells that have a conventional directory say what the page
// says: a directory the person owns. And the script is not printed to a
// terminal: it is two hundred lines of shell that nobody reads on a screen and
// that scrolls the one instruction off it. On a terminal the command shows the
// steps; given a pipe or a file, which is how it is used, it writes the script.
var completionRecipes = map[string]string{
	"zsh": `Writes the zsh completion script to standard output. Put it in a directory you own, on fpath before compinit runs:

  mkdir -p ~/.zsh/completions
  rta completion zsh > ~/.zsh/completions/_rta
  # once, in ~/.zshrc, above the compinit line:
  #   fpath=(~/.zsh/completions $fpath)

Restart your shell afterwards. With Homebrew, its own directory is already on fpath:

  rta completion zsh > $(brew --prefix)/share/zsh/site-functions/_rta`,

	"bash": `Writes the bash completion script to standard output. bash-completion loads this directory on its own:

  mkdir -p ~/.local/share/bash-completion/completions
  rta completion bash > ~/.local/share/bash-completion/completions/rta

Restart your shell afterwards.`,

	"fish": `Writes the fish completion script to standard output:

  rta completion fish > ~/.config/fish/completions/rta.fish

Restart your shell afterwards.`,
}

// shapeCompletion gives the completion commands their recipe, and keeps their
// script off a terminal. The recipe is each command's long text, so --help and
// the bare command say the same thing and cannot drift apart.
//
// The shell cobra adds that rta documents no recipe for, powershell, is removed:
// the platforms are macOS and Linux, where the three above are what people run.
//
// The script is written to the command's own output, not the one cobra captured
// when it built the command: that one is read before the tree is handed to a
// caller, so a caller that sets its output afterwards — a test, an embedder —
// would see the script go to the process's stdout instead.
func shapeCompletion(root *cobra.Command) {
	scripts := map[string]func(*cobra.Command, io.Writer, bool) error{
		"zsh": func(c *cobra.Command, w io.Writer, noDesc bool) error {
			if noDesc {
				return c.Root().GenZshCompletionNoDesc(w)
			}
			return c.Root().GenZshCompletion(w)
		},
		"bash": func(c *cobra.Command, w io.Writer, noDesc bool) error {
			return c.Root().GenBashCompletionV2(w, !noDesc)
		},
		"fish": func(c *cobra.Command, w io.Writer, noDesc bool) error {
			return c.Root().GenFishCompletion(w, !noDesc)
		},
	}
	for _, group := range root.Commands() {
		if group.Name() != "completion" {
			continue
		}
		for _, shell := range group.Commands() {
			recipe, ok := completionRecipes[shell.Name()]
			if !ok {
				group.RemoveCommand(shell)
				continue
			}
			shell.Long = recipe
			script := scripts[shell.Name()]
			shell.RunE = func(cmd *cobra.Command, _ []string) error {
				if isTTY() {
					var steps strings.Builder
					newHelper(helpWidth()).prose(&steps, cmd.Long)
					_, err := fmt.Fprintln(cmd.OutOrStdout(), strings.TrimRight(steps.String(), "\n"))
					return err
				}
				noDescriptions, _ := cmd.Flags().GetBool("no-descriptions")
				return script(cmd, cmd.OutOrStdout(), noDescriptions)
			}
		}
	}
}
