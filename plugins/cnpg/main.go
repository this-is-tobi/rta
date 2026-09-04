// Command rta-plugin-cnpg reads the state of CloudNativePG PostgreSQL
// clusters: which ones exist, and everything one of them will tell you about
// its own health, replication, backups and storage.
//
// Build it and put it on your $PATH as `rta-plugin-cnpg`:
//
//	cd plugins/cnpg && go build -o ~/.local/bin/rta-plugin-cnpg .
//
// It needs `kubectl` and nothing else — no address to configure, no
// credential to state. Whatever cluster kubectl can already reach, this can
// read, through the same kubeconfig, contexts and exec credential plugins the
// operator already keeps working.
//
// # Why this exists next to `kubectl cnpg`
//
// CloudNativePG ships its own kubectl plugin and its `status` is excellent.
// It also does considerably more than read one custom resource — it reaches
// into pods — and that is the part that stops working behind proxies people
// put in front of clusters, where a plain API read goes through untouched.
// This asks one GET and derives the same answers from it, so it works
// wherever `kubectl get` works. Where both work, use theirs: it knows things
// only an exec into the pod can know.
//
// # What it deliberately does not do
//
// No switchover, no promotion, no restart, no destroy. Every one of those is
// an operation on a production database that can take it down, and this
// plugin holds the line plugins/kube holds against `kubectl`'s mutating half:
// a read-first fast path is a different thing from a control plane, and the
// tool that already does it well is one command away.
//
// # The one thing it writes
//
// This list once ended "no backup trigger", and said every capability here
// was Safety: Read. `cnpg.backup.request` ends that, so the reasoning is
// written out rather than quietly dropped.
//
// A backup is the one member of that list whose failure mode is not an
// outage. Switchover, promotion, restart and destroy can all take a database
// away; a backup costs I/O and object-store space. More to the point, it is
// the one that **chooses nothing**: a Backup object carries a cluster
// reference and no destination, so where the bytes go is whatever that
// cluster's own configuration already said. Every gate elsewhere in rta
// exists because a caller could name a place — `net.probe` and `cert.expiry`
// were closed for exactly that — and here there is no place to name.
//
// It is still gated twice over. Safety: Write keeps it off the default MCP
// surface entirely, so an agent sees it only after an operator passes
// `--allow-write cnpg`; NeedsGrant then makes each call a decision that
// names the cluster and expires on its own. The blanket claim that used to
// stand here — "an agent granted this can change nothing" — was a simpler
// sentence than the boundary's actual answer, and the actual answer is the
// one that holds: nothing reaches an agent that an operator did not turn on
// and then grant.
package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func main() { sdk.Serve(Plugin()) }

// connFields is the connection half of every capability's inputs.
//
// `context` is Local for the reason plugins/kube states at length and which
// is worth restating because it is the security property of this plugin:
// **choosing which cluster a call reaches is choosing a destination, and a
// remote caller may never choose a destination.** A kubeconfig is a list of
// every cluster this machine can reach with a working identity attached to
// each; an agent free to pass `--context` would not be reading "the cluster
// the operator is working in" but any of them, production included. So config
// fills it, a person at a terminal passes it, and MCP cannot.
//
// `namespace` is not Local, and the difference is the point: a namespace is a
// record inside a cluster somebody has already chosen.
func connFields() []plugin.Field {
	return []plugin.Field{
		{Name: "context", Type: plugin.String, Config: "context", Local: true,
			Help:    "kubeconfig context to use — the current one when omitted",
			Suggest: suggestContexts},
	}
}

func nsFields() []plugin.Field {
	return []plugin.Field{
		// Live, because the Suggest contacts the cluster: that read must be
		// something a completion press asked for, never something typing
		// caused. plugins/kube pins the same rule on its own namespace field.
		{Name: "namespace", Type: plugin.String,
			Help: "namespace to read — the context's own when omitted",
			Live: true, Suggest: suggestNamespaces},
	}
}

// cap assembles a capability's inputs in the order a person reads them, and
// sets NoPreview on all of them: each reaches off the box, so none belongs on
// a dashboard that refreshes on a timer without somebody having said so.
func cap(c plugin.Capability, own ...plugin.Field) plugin.Capability {
	c.Inputs = append(append(own, c.Inputs...), append(nsFields(), connFields()...)...)
	c.NoPreview = true
	return c
}

// version is what this build claims to be, stamped by whatever built it:
// `-X main.version=`, which is the Makefile's flag and GoReleaser's own
// default. A build nobody stamped says "dev" rather than claiming a release
// number that was never cut — an index entry carries this verbatim, and a
// version is a fact about a release, not about the source it came from.
var version = "dev"

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "cnpg",
		Summary: "Read the state of CloudNativePG PostgreSQL clusters",
		Version: version,
		// Everything this plugin does is a kubectl call, and kubectl cannot
		// make one without the kubeconfig. Declaring it is a request, not a
		// grant: rta denies credential locations to every plugin by default,
		// and `rta plugin allow cnpg` is where an operator decides — against
		// this artifact's digest, so a rebuild asks again.
		Needs: []plugin.Need{plugin.NeedKubeconfig},
		Capabilities: []plugin.Capability{
			cap(plugin.Capability{
				ID: "cnpg.list", Summary: "Every CloudNativePG cluster, and whether it is healthy",
				Safety: plugin.Read, Idempotent: true,
				Description: "One `kubectl get clusters.postgresql.cnpg.io -o json`, rendered " +
					"with the columns the CRD itself declares as printer columns — so this and " +
					"`kubectl get` answer the same question the same way. Ready is " +
					"ready/desired instances; a cluster short of its own spec is graded as a " +
					"problem whatever its phase says. Reads one custom resource and nothing " +
					"else: no pods, no exec, no logs.",
				Inputs: []plugin.Field{
					{Name: "all-namespaces", Type: plugin.Bool,
						Help: "every namespace instead of one"},
				},
				Run: runList,
			}),
			cap(plugin.Capability{
				ID: "cnpg.status", Summary: "One cluster in depth: instances, replication, backups, storage",
				Safety: plugin.Read, Idempotent: true,
				Description: "Everything the Cluster resource reports about itself, laid out as " +
					"the questions somebody opens it to ask. Instances are listed primary first, " +
					"with the role taken from the cluster's own currentPrimary rather than each " +
					"instance's self-report — during a failover those disagree, and the " +
					"cluster's view is the one the rest of the fields are consistent with. " +
					"A switchover in flight is derived rather than printed raw: CNPG moves " +
					"targetPrimary before currentPrimary, so the two differing means a " +
					"promotion is happening now. Every instance on one node is reported, " +
					"because the CRD's own documentation calls that the absence of high " +
					"availability. Conditions are shown only when they are not satisfied. " +
					"The last successful backup is reported as an age, since that is the form " +
					"the question is asked in — and a backup that is not configured at all is " +
					"distinguished from one that is configured and failing, which the resource " +
					"spells identically. The primary's tenure is derived (a young primary on an " +
					"old cluster is the trace of a failover), certificate expiries are graded " +
					"against the same 30-day window rta's other certificate checks use, and the " +
					"replication posture, resource bounds and superuser-access switch are read " +
					"from the spec.",
				Inputs: []plugin.Field{
					{Name: "name", Type: plugin.String, Positional: true, Required: true,
						Help: "the cluster to read",
						Live: true, Suggest: suggestClusters},
				},
				Run: runStatus,
			}),
			backupListCapability(),
			backupRequestCapability(),
			storageCapability(),
		},
	}
}

func runList(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	var list clusterList
	if verr := getJSON(ctx, s, "", &list); verr != nil {
		return nil, verr
	}
	if len(list.Items) == 0 {
		return view.Text{Body: "No CloudNativePG clusters in " + s.where() + "."}, nil
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Namespace"}, {Name: "Name"},
		{Name: "Ready"}, {Name: "Status", Kind: view.KindStatus},
		{Name: "Primary"}, {Name: "Phase"}, {Name: "Age"},
	}}
	for _, c := range list.Items {
		status := "ok"
		if !c.healthy() {
			status = "warn"
		}
		if c.Status.PrimaryFailingSince != nil || c.Status.CurrentPrimary == "" {
			status = "fail"
		}
		t.Rows = append(t.Rows, []string{
			c.Metadata.Namespace, c.Metadata.Name, c.ready(), status,
			orDash(c.Status.CurrentPrimary), orDash(c.Status.Phase),
			age(c.Metadata.CreationTimestamp),
		})
	}
	t.Total = len(t.Rows)
	return t, nil
}

func runStatus(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	name := req.String("name")
	if verr := checkName("cluster", name); verr != nil {
		return nil, verr
	}
	var c cluster
	if verr := getJSON(ctx, s, name, &c); verr != nil {
		return nil, verr
	}
	return statusView(c), nil
}

// statusView lays one cluster out as three sections: what it is, what its
// instances are doing, and what is wrong with it.
//
// The third section is absent when there is nothing in it, which is the same
// conditional-column doctrine the rest of rta follows one level up: a
// "Problems" heading with nothing under it trains people to skip the heading.
func statusView(c cluster) view.View {
	overview := []view.Pair{
		{Key: "Cluster", Value: c.Metadata.Namespace + "/" + c.Metadata.Name},
		{Key: "Phase", Value: orDash(c.Status.Phase)},
	}
	if c.Status.PhaseReason != "" {
		overview = append(overview, view.Pair{Key: "Reason", Value: c.Status.PhaseReason})
	}
	overview = append(overview,
		view.Pair{Key: "Instances", Value: c.ready() + " ready"},
		view.Pair{Key: "Primary", Value: primaryLine(c)},
	)
	// The major version answers "what am I actually running" one field
	// earlier than the image tag does; when the operator is too old to report
	// it, the image row carries what there is.
	if info := c.Status.PGDataImageInfo; info.MajorVersion > 0 {
		overview = append(overview, view.Pair{Key: "PostgreSQL",
			Value: strconv.Itoa(info.MajorVersion) + " — " +
				firstNonEmpty(info.Image, c.Status.Image, c.Spec.ImageName)})
	} else {
		overview = append(overview, view.Pair{
			Key: "Image", Value: orDash(firstNonEmpty(c.Status.Image, c.Spec.ImageName))})
	}
	overview = append(overview,
		view.Pair{Key: "Storage", Value: c.storageLine()},
	)
	if r := c.resourceLine(); r != "" {
		overview = append(overview, view.Pair{Key: "Resources", Value: r})
	}
	overview = append(overview,
		view.Pair{Key: "Replication", Value: c.replicationLine()},
	)
	if e := c.Spec.EnableSuperuserAccess; e != nil {
		v := "disabled"
		if *e {
			v = "enabled"
		}
		overview = append(overview, view.Pair{Key: "Superuser access", Value: v})
	}
	overview = append(overview,
		view.Pair{Key: "Timeline", Value: strconv.Itoa(c.Status.TimelineID)},
		view.Pair{Key: "Backup", Value: backupLine(c)},
	)
	// Beside the backup row, because it is the half that decides what a backup
	// is worth: a base backup recovers to the moment it was taken, and
	// everything after that comes from archived WAL.
	if line := c.archivingLine(); line != "" {
		overview = append(overview, view.Pair{Key: "WAL archiving", Value: line})
	}
	if name, at, ok := c.soonestCert(); ok {
		v := "soonest expires in " + until(at) + " (" + name + ")"
		if time.Until(at) <= 0 {
			v = name + " expired " + age(at) + " ago"
		}
		overview = append(overview, view.Pair{Key: "Certificates", Value: v})
	}
	if c.Status.FirstRecoverabilityPoint != "" {
		overview = append(overview, view.Pair{
			Key: "Recoverable from", Value: c.Status.FirstRecoverabilityPoint})
	}

	instances := view.Table{Columns: []view.Column{
		{Name: "Instance"}, {Name: "Role"}, {Name: "State"}, {Name: "IP"}, {Name: "Timeline"},
	}}
	for _, r := range c.instances() {
		instances.Rows = append(instances.Rows,
			[]string{r.name, r.role, r.state, r.ip, strconv.Itoa(r.timeline)})
	}
	instances.Total = len(instances.Rows)

	sections := []view.Section{
		{Title: "Cluster", View: view.KeyValue{Pairs: overview}},
		{Title: "Instances", View: instances},
	}
	// Both settings sections come out of the read that already happened.
	// **This capability still makes exactly one GET**, which is the property
	// its own doc comment claims and the reason it works where `kubectl cnpg
	// status` does not — so what a cluster states about itself belongs here,
	// and what needs a second resource (its volumes, its backups) is its own
	// capability rather than a second round trip hidden inside this one.
	if s := settingsTable(c, walSettings); len(s.Rows) > 0 {
		sections = append(sections, view.Section{
			Title: "Write-ahead log and recovery", View: s})
	}
	if s := settingsTable(c, serverSettings); len(s.Rows) > 0 {
		sections = append(sections, view.Section{Title: "Server settings", View: s})
	}
	if problems := problemTable(c); len(problems.Rows) > 0 {
		sections = append(sections, view.Section{Title: "Needs attention", View: problems})
	}
	return view.Sections{Items: sections}
}

// settingsTable renders the parameters a cluster states from one of the
// curated lists, and says what it left to CloudNativePG.
//
// The unstated half is a row rather than an omission, because "max_connections
// is CloudNativePG's default" and "max_connections is 100" are different
// answers and only the first is one rta can stand behind — the CRD publishes
// no defaults, and somebody sizing a connection pool against a number this
// page made up would be sizing against nothing.
func settingsTable(c cluster, want []struct{ key, means string }) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "Setting"}, {Name: "Value"}, {Name: "What it decides"},
	}}
	for _, row := range c.settings(want) {
		t.Rows = append(t.Rows, []string{row.key, row.value, row.means})
	}
	if len(t.Rows) == 0 {
		return t
	}
	if unstated := c.unstatedSettings(want); len(unstated) > 0 {
		t.Rows = append(t.Rows, []string{
			"not stated", strings.Join(unstated, ", "),
			"left at CloudNativePG's own defaults, which the resource does not publish",
		})
	}
	t.Total = len(t.Rows)
	return t
}

// certWarnDays mirrors builtin/internal/x509check.DefaultWarnDays — restated
// rather than imported, because this plugin is its own module and that
// package is internal to the core binary. If the two ever disagree, the core
// value is the one to follow: every certificate rta grades should go amber on
// the same day.
const certWarnDays = 30

// problemTable is everything worth acting on, and is empty on a healthy
// cluster.
func problemTable(c cluster) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "What"}, {Name: "Status", Kind: view.KindStatus}, {Name: "Detail"},
	}}
	add := func(what, status, detail string) {
		t.Rows = append(t.Rows, []string{what, status, detail})
	}

	// **No primary is the worst state a database cluster has**, and the first
	// version of this table had no case for it: the list view graded it fail
	// from the same field, and the page somebody opens to find out *why* said
	// nothing at all. Found by writing the test for every field and asking
	// what each one's absence means.
	switch {
	case c.Status.CurrentPrimary == "":
		add("primary", "fail", "there is no primary — nothing can be written to this cluster")
	case c.Status.PrimaryFailingSince != nil:
		add("primary", "fail", "unhealthy since "+age(*c.Status.PrimaryFailingSince)+" ago")
	}
	if c.switchingOver() {
		add("switchover", "warn", "promoting "+c.Status.TargetPrimary+
			" — the primary is still "+c.Status.CurrentPrimary)
	}
	if !c.healthy() {
		add("instances", "warn", c.ready()+" ready — the cluster is short of its own spec")
	}
	if c.singleNode() {
		add("topology", "warn", "every instance is on one node, so nothing survives losing it")
	}
	// **Divergence rather than lag, and the difference is the finding.** An
	// instance on another timeline is not behind and will not catch up: it
	// followed a history the cluster has abandoned, which is what a promotion
	// an instance missed leaves behind. Live replication lag is not in this
	// resource at all — see settings.go for why this plugin does not go and
	// get it — so this is the desync question the single read can answer, and
	// it happens to be the one that does not resolve itself.
	if diverged := c.divergedInstances(); len(diverged) > 0 {
		add("timeline", "fail", strings.Join(diverged, ", ")+" reports a timeline other "+
			"than the cluster's "+strconv.Itoa(c.Status.TimelineID)+
			" — a diverged instance does not catch up")
	}
	// Archiving off is not a backup finding: a cluster can have a perfectly
	// good base backup and still only recover to the instant it was taken.
	if c.Spec.PostgreSQL.Parameters["archive_mode"] == "off" && c.backupConfigured() {
		add("WAL archiving", "warn", "off, while a backup is configured — a restore "+
			"recovers to the backup's own instant and no further")
	}
	// Three backup findings, one row: "nothing is configured", "configured
	// and never once worked", and "worked before, failing now" are different
	// conversations with different people, and the old single message ("no
	// successful backup has ever been recorded") collapsed the first two —
	// sending somebody to debug a backup job that does not exist.
	success, ever := c.lastSuccessfulBackup()
	lastFail, failedOnce := parseWhen(c.Status.LastFailedBackup)
	switch {
	case !ever && !c.backupConfigured():
		add("backup", "warn", "not configured — nothing backs this cluster up")
	case !ever:
		msg := "configured, but no backup has ever succeeded"
		if failedOnce {
			msg += " — the last attempt failed " + age(lastFail) + " ago"
		}
		add("backup", "warn", msg)
	case failedOnce && lastFail.After(success):
		add("backup", "warn", "the most recent attempt failed "+age(lastFail)+
			" ago — the last success is "+age(success)+" old")
	}
	if name, at, ok := c.soonestCert(); ok {
		switch {
		case time.Until(at) <= 0:
			add("certificates", "fail", name+" expired "+age(at)+
				" ago — TLS connections to this cluster are failing or about to")
		case time.Until(at) < certWarnDays*24*time.Hour:
			// The operator rotates these certificates itself, so one this
			// close to expiry is not a renewal somebody forgot — it means the
			// rotation is not happening, which is an operator problem wearing
			// a certificate's clothes.
			add("certificates", "warn", name+" expires in "+until(at)+
				" — the operator should have rotated it already")
		}
	}
	for _, pvc := range []struct {
		what  string
		names []string
	}{
		{"dangling PVCs", c.Status.DanglingPVC},
		{"unusable PVCs", c.Status.UnusablePVC},
		{"resizing PVCs", c.Status.ResizingPVC},
	} {
		if len(pvc.names) > 0 {
			add(pvc.what, "warn", joinClipped(pvc.names))
		}
	}
	for _, cond := range c.notTrue() {
		detail := cond.Reason
		if cond.Message != "" {
			detail += ": " + cond.Message
		}
		add(cond.Type, "warn", orDash(detail))
	}
	t.Total = len(t.Rows)
	return t
}

func primaryLine(c cluster) string {
	if c.Status.CurrentPrimary == "" {
		return "none — the cluster has no primary right now"
	}
	if c.switchingOver() {
		return c.Status.CurrentPrimary + " → " + c.Status.TargetPrimary + " (switching over)"
	}
	line := c.Status.CurrentPrimary
	// The tenure is the failover trace: a cluster whose primary is hours old
	// on a resource that is months old changed primaries recently, and this
	// is the only line that says so.
	if tenure, ok := c.primaryFor(); ok {
		line += " — primary for " + tenure
	}
	return line
}

func backupLine(c cluster) string {
	if at, ever := c.backupAge(); ever {
		return "last succeeded " + at + " ago"
	}
	return "none recorded"
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// joinClipped names a few and counts the rest, so a cluster with forty stuck
// PVCs produces a finding rather than a wall.
func joinClipped(names []string) string {
	const max = 3
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:max], ", ") + " and " + strconv.Itoa(len(names)-max) + " more"
}
