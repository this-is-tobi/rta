package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The kubeconfig half. Everything here reads `kubectl config`, which answers
// from the file alone — no cluster is contacted, so these keep working when
// every cluster in the file is unreachable, and that is exactly when somebody
// is most likely to be asking.

// kubeconfig is the shape of `kubectl config view -o json`, cut down to what
// is read here.
type kubeconfig struct {
	CurrentContext string `json:"current-context"`
	Contexts       []struct {
		Name    string `json:"name"`
		Context struct {
			Cluster   string `json:"cluster"`
			User      string `json:"user"`
			Namespace string `json:"namespace"`
		} `json:"context"`
	} `json:"contexts"`
	Clusters []struct {
		Name    string `json:"name"`
		Cluster struct {
			Server string `json:"server"`
		} `json:"cluster"`
	} `json:"clusters"`
}

// readConfig loads the kubeconfig as kubectl itself resolves it.
//
// Through `kubectl config view` rather than by parsing ~/.kube/config
// directly, and that is not laziness: KUBECONFIG may name several files to be
// merged, in an order with rules, and reimplementing that merge is a way to
// show an operator a current-context that is not the one their next command
// will use.
func readConfig(ctx context.Context) (kubeconfig, *view.Error) {
	// `--raw` is deliberately NOT passed. Without it kubectl redacts
	// credentials — client certificates, bearer tokens, exec plugin output —
	// and none of them are any of this plugin's business. The unredacted form
	// would put a working credential in a plugin's memory, in its gRPC
	// response, and one careless view away from a model's context.
	raw, verr := run(ctx, "config", "view", "-o", "json")
	if verr != nil {
		return kubeconfig{}, verr
	}
	var cfg kubeconfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return kubeconfig{}, view.Errorf("kube.unreadable",
			"this machine's kubeconfig could not be read: %v", err)
	}
	return cfg, nil
}

func (k kubeconfig) server(cluster string) string {
	for _, c := range k.Clusters {
		if c.Name == cluster {
			return c.Cluster.Server
		}
	}
	return ""
}

func (k kubeconfig) find(name string) (string, string, string, bool) {
	for _, c := range k.Contexts {
		if c.Name == name {
			return c.Context.Cluster, c.Context.User, c.Context.Namespace, true
		}
	}
	return "", "", "", false
}

func runContextList(ctx context.Context, _ plugin.Request) (view.View, error) {
	cfg, verr := readConfig(ctx)
	if verr != nil {
		return nil, verr
	}
	rows := make([][]string, 0, len(cfg.Contexts))
	for _, c := range cfg.Contexts {
		current := ""
		if c.Name == cfg.CurrentContext {
			current = "current"
		}
		ns := c.Context.Namespace
		if ns == "" {
			ns = "default"
		}
		rows = append(rows, []string{
			current, c.Name, c.Context.Cluster, ns, cfg.server(c.Context.Cluster),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][1] < rows[j][1] })
	return view.Table{
		Columns: []view.Column{
			{Name: "", Kind: view.KindStatus}, {Name: "context"},
			{Name: "cluster"}, {Name: "namespace"}, {Name: "server"},
		},
		Rows: rows, Total: len(rows),
	}, nil
}

func runContextGet(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	cfg, verr := readConfig(ctx)
	if verr != nil {
		return nil, verr
	}
	name := s.Context
	if name == "" {
		name = cfg.CurrentContext
	}
	if name == "" {
		return nil, view.Errorf("kube.context.none",
			"this machine's kubeconfig names no current context").
			WithHint("`rta kube context set <name>` picks one; `rta kube context list` shows them")
	}
	cluster, user, ns, ok := cfg.find(name)
	if !ok {
		return nil, view.Errorf("kube.context.unknown", "no context named %q", name).
			WithHint("`rta kube context list` shows the contexts this machine has")
	}
	if ns == "" {
		ns = "default"
	}
	pairs := []view.Pair{
		{Key: "context", Value: name},
		{Key: "current", Value: yesNo(name == cfg.CurrentContext)},
		{Key: "cluster", Value: cluster},
		{Key: "server", Value: cfg.server(cluster)},
		{Key: "namespace", Value: ns},
		// The identity's *name*, which is a label in the operator's own file
		// and not a credential. Nothing here reads what is behind it — see
		// readConfig on why --raw is not passed.
		{Key: "user", Value: user},
	}
	return view.KeyValue{Pairs: pairs}, nil
}

// runContextSet is the one mutation in this plugin.
//
// It rewrites current-context and nothing else, through `kubectl config
// use-context`, which is the same code path the operator's own command takes.
// Writing the file here instead would mean reproducing kubectl's merge rules
// for a multi-file KUBECONFIG — and getting that wrong does not fail loudly,
// it silently rewrites the wrong file or flattens a merge the operator built
// on purpose.
func runContextSet(ctx context.Context, req plugin.Request) (view.View, error) {
	name := strings.TrimSpace(req.String("name"))
	if verr := checkName("context", name); verr != nil {
		return nil, verr
	}
	if name == "" {
		return nil, view.Errorf("kube.context.empty", "name a context to switch to")
	}
	cfg, verr := readConfig(ctx)
	if verr != nil {
		return nil, verr
	}
	// Checked before the switch rather than left to kubectl, because the
	// answer is more useful here: this can list what does exist, and a
	// dry-run has to be able to say what it *would* do without doing it.
	if _, _, _, ok := cfg.find(name); !ok {
		return nil, view.Errorf("kube.context.unknown", "no context named %q", name).
			WithHint("`rta kube context list` shows the contexts this machine has: " + names(cfg))
	}
	if cfg.CurrentContext == name {
		// Idempotent, and said rather than silently re-run: an operator who
		// asked for a switch that was already done should be told it was
		// already done, not shown a success that implies something moved.
		return view.Text{Body: fmt.Sprintf("%s is already the current context", name)}, nil
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf(
			"would switch this machine's current context from %s to %s — every later kubectl "+
				"command on this machine would follow it", currentOr(cfg), name)}, nil
	}
	if _, verr := run(ctx, "config", "use-context", name); verr != nil {
		return nil, verr
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "current context", Value: name},
		{Key: "was", Value: currentOr(cfg)},
		{Key: "applies to", Value: "every later command on this machine that reads this kubeconfig"},
	}}, nil
}

func currentOr(cfg kubeconfig) string {
	if cfg.CurrentContext == "" {
		return "none"
	}
	return cfg.CurrentContext
}

func names(cfg kubeconfig) string {
	out := make([]string, 0, len(cfg.Contexts))
	for _, c := range cfg.Contexts {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// suggestContexts completes a context name from the kubeconfig.
//
// Reads the file and contacts nothing, which is what makes it safe to run on
// a keypress: a completion must not be an action,
// and this one is a local read.
func suggestContexts(ctx context.Context, _ plugin.Request) []string {
	cfg, verr := readConfig(ctx)
	if verr != nil {
		return nil
	}
	out := make([]string, 0, len(cfg.Contexts))
	for _, c := range cfg.Contexts {
		label := c.Context.Cluster
		if c.Name == cfg.CurrentContext {
			label += " (current)"
		}
		out = append(out, c.Name+"\t"+label)
	}
	sort.Strings(out)
	return out
}
