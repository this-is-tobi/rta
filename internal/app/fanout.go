package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A read that names an environment and no target in it runs on every target
// the environment holds for that plugin, one section each.
//
// `ohmlab` is the environment and `ohmlab/crowdsec` is one target of it, which
// is how the dashboard already reads a bare name: one panel per connection. A
// call that said `rta cnpg list` with ohmlab switched on, or `--profile ohmlab`,
// used to be refused for holding five cnpg connections, and the person who had
// chosen the environment was sent to type the list back.
//
// What keeps this from being a guess is what it is allowed to be:
//
//   - **Reads only.** A write or a destructive call names its target, as it
//     always did: a change that goes to every connection of an environment
//     because one word was left out is the mistake the refusal exists for.
//   - **A person's command line only.** An agent's grant names one connection
//     and the MCP surface never reaches this code; the TUI's forms ask which.
//   - **Each target is its own call.** Its own fence, its own credential, its
//     own forward, opened and closed around it, and a failure in one is a
//     warning on the page that the others still fill, with a non-zero exit so a
//     script cannot take a partial answer for a whole one.
//   - **Said on the page.** The sections are titled with the target, so what
//     ran where is in the output and in a pipe.
//
// Not for a call that typed a connection of its own (`--host`), which has said
// where it goes, nor one that left the environment (`--no-profile`).

// fanOutTargets is the targets a call fans out to, or nil when it does not.
func fanOutTargets(cmd *cobra.Command, c plugin.Capability, values map[string]any, opts *globalOpts) []string {
	if c.Safety != plugin.Read || !plugin.Profilable(c) || noProfile(cmd) {
		return nil
	}
	explicit := ""
	if f := cmd.Flags().Lookup("profile"); f != nil {
		explicit = strings.TrimSpace(f.Value.String())
	}
	if strings.Contains(explicit, "/") {
		return nil
	}
	if _, stated, verr := adHocConnection(cmd, c, explicit); verr != nil || stated {
		return nil
	}
	name := explicit
	if name == "" {
		name = profile.Active()
	}
	if name == "" {
		return nil
	}
	cfg, err := opts.loadedConfig()
	if err != nil {
		return nil
	}
	p, ok := cfg.Profiles[config.RefName(name)]
	if !ok {
		return nil
	}
	ns := plugin.Namespace(c.ID)
	if !p.Ambiguous(ns) {
		return nil
	}
	if typedWhatTargetsSet(p, c, ns, values) {
		return nil
	}
	return profile.InstanceRefs(p, name, ns)
}

// typedWhatTargetsSet is whether the call typed a value for an input that the
// targets state themselves — `--host`, `--namespace` — or that a forward fills.
// One typed value would then go to every target in place of what each says, N
// calls to one place, and that is the call that has to name its target.
func typedWhatTargetsSet(p config.Profile, c plugin.Capability, ns string, values map[string]any) bool {
	for _, f := range c.Inputs {
		if _, typed := values[f.Name]; !typed {
			continue
		}
		key := f.Config
		if key == "" {
			key = f.Name
		}
		for _, label := range p.Instances(ns) {
			_, conn, _ := p.ForInstance(ns, label)
			if _, set := conn.Set[key]; set {
				return true
			}
			if (conn.Kube != "" || conn.SSH != "") && (f.Name == "host" || f.Name == "port") {
				return true
			}
		}
	}
	return false
}

// runFanOut runs the capability once for each target and renders what came
// back as one page.
func runFanOut(ctx context.Context, cmd *cobra.Command, c plugin.Capability, values map[string]any,
	opts *globalOpts, renderOpts cli.Options, targets []string,
) error {
	flag := cmd.Flags().Lookup("profile")
	if flag == nil {
		return nil
	}
	was := flag.Value.String()
	defer func() { _ = flag.Value.Set(was) }()

	page := view.Sections{}
	failed := 0
	for _, target := range targets {
		_ = flag.Value.Set(target)
		v, verr := runOnTarget(ctx, cmd, c, values, opts)
		if verr != nil {
			failed++
			w := *verr
			w.Message = target + ": " + w.Message
			// The input that target's refusal names is one of the call made for
			// it, and the page is every target's: no input is the page's to fix.
			w.Input = ""
			page.Warnings = append(page.Warnings, w)
		}
		// A failure that carried its answer is that target's section all the
		// same, as the single call draws it before its refusal.
		if v != nil {
			page.Items = append(page.Items, view.Section{Title: target, View: v})
		}
	}
	if err := cli.Render(cmd.OutOrStdout(), page, renderOpts); err != nil {
		return err
	}
	if failed == 0 {
		return nil
	}
	partial := &view.Error{
		Code:    "core.profile.fanout.failed",
		Message: fmt.Sprintf("%d of %d targets failed", failed, len(targets)),
		Hint:    "the page says which and why; name one to run it alone, for example `--profile " + targets[0] + "`",
	}
	_ = cli.RenderError(cmd.ErrOrStderr(), partial, renderOpts)
	return Rendered(partial)
}

// runOnTarget is the single-target path of runCapability for a read: the
// target's connection resolved, its forward opened for this call and closed
// after it, then the run, which is runCapability's own (preparedRun, ranAs).
// A refusal can come back with a view beside it, the answer a failure carried.
func runOnTarget(ctx context.Context, cmd *cobra.Command, c plugin.Capability, values map[string]any,
	opts *globalOpts,
) (view.View, *view.Error) {
	bound, closeTunnel, verr := resolveProfile(ctx, cmd, c, values, opts)
	defer closeTunnel()
	if verr != nil {
		return nil, verr
	}
	_, req, verr := preparedRun(c, values, bound, opts)
	if verr != nil {
		return nil, verr
	}
	v, err := c.Run(ctx, req)
	return ranAs(c, v, err, values, bound, opts)
}
