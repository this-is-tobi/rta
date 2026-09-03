package main

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The slice of the CloudNativePG Backup resource this reads, and the one it
// writes.
//
// Every field below was read off the CRD schema on a running operator
// (`kubectl get crd backups.postgresql.cnpg.io -o json`) rather than from
// memory — the same standard cluster.go holds itself to, and the reason it
// matters here is sharper: this is the one place in the plugin that *creates*
// an object, and a field invented from recall is a document the API server
// either rejects or, worse, accepts and ignores.
//
// **The credential fields are deliberately not decoded, and that is a rule
// rather than an omission.** A Backup's status carries `s3Credentials`,
// `azureCredentials`, `googleCredentials` and `endpointCA` — each an object of
// secret *references* rather than secret values, which is why reading them
// would leak nothing today. It is skipped anyway, on cluster.go's own
// principle that what is not decoded cannot be misinterpreted: the names of
// the secrets holding a cluster's object-store keys are not part of any
// question somebody opens a backup listing to ask, and a struct that does not
// have them cannot grow a view that prints them.

// backupCRD is the fully-qualified resource name, for the reason clusterCRD
// spells out: `backup` as a short name means whatever the cluster's discovery
// document says it means today.
const backupCRD = "backups.postgresql.cnpg.io"

type backupList struct {
	Items []backupObject `json:"items"`
}

type backupObject struct {
	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace"`
		CreationTimestamp time.Time         `json:"creationTimestamp"`
		Labels            map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Cluster struct {
			Name string `json:"name"`
		} `json:"cluster"`
		Method string `json:"method,omitempty"`
		Target string `json:"target,omitempty"`
		// A pointer for the reason EnableSuperuserAccess is one: absent means
		// "whatever .spec.backup.volumeSnapshot.online says", which is not
		// the same statement as false, and encoding a guess would silently
		// turn somebody's hot backup cold.
		Online *bool `json:"online,omitempty"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
		Error string `json:"error"`
		// Method is echoed back resolved: the spec's may be empty, meaning
		// "the cluster decides", and this is what the cluster decided.
		Method          string `json:"method"`
		BackupID        string `json:"backupId"`
		BackupName      string `json:"backupName"`
		DestinationPath string `json:"destinationPath"`
		ServerName      string `json:"serverName"`
		StartedAt       string `json:"startedAt"`
		StoppedAt       string `json:"stoppedAt"`
		BeginWal        string `json:"beginWal"`
		EndWal          string `json:"endWal"`
		InstanceID      struct {
			PodName string `json:"podName"`
		} `json:"instanceID"`
	} `json:"status"`
}

// The phases a Backup passes through.
//
// The CRD declares `status.phase` as a bare string with no enum, so these are
// matched rather than exhausted: an unrecognised phase renders as itself and
// grades neutral, which is the behaviour that survives the operator learning a
// new one.
const (
	phaseCompleted = "completed"
	phaseFailed    = "failed"
)

func (b backupObject) failed() bool { return strings.EqualFold(b.Status.Phase, phaseFailed) }
func (b backupObject) done() bool   { return strings.EqualFold(b.Status.Phase, phaseCompleted) }

// status grades one backup for the table's status column.
func (b backupObject) status() string {
	switch {
	case b.failed():
		return "fail"
	case b.done():
		return "ok"
	}
	// Running, pending, walArchivingLaunched — in flight is not a problem,
	// and colouring it warn would make every backup look wrong while it works.
	return "info"
}

// took is how long the backup ran, or how long it has been running.
func (b backupObject) took() string {
	start, ok := parseWhen(b.Status.StartedAt)
	if !ok {
		return "—"
	}
	if stop, ended := parseWhen(b.Status.StoppedAt); ended {
		return span(stop.Sub(start))
	}
	return span(time.Since(start)) + " so far"
}

// where names the destination, and says who chose it.
//
// The destination is never rta's to choose and never a caller's: a Backup
// carries a cluster reference and nothing else, so where the bytes land is
// whatever that cluster's own configuration already said. This row exists to
// make that visible rather than to be configurable.
func (b backupObject) where() string {
	path := strings.TrimSpace(b.Status.DestinationPath)
	if path == "" {
		if m := b.resolvedMethod(); m == "volumeSnapshot" {
			return "a volume snapshot in this cluster"
		}
		return "—"
	}
	if s := strings.TrimSpace(b.Status.ServerName); s != "" {
		return path + " (server " + s + ")"
	}
	return path
}

// resolvedMethod prefers what the operator decided over what was asked for,
// because an empty spec.method means "the cluster decides" and the answer to
// "how was this taken" is the decision, not the blank.
func (b backupObject) resolvedMethod() string {
	if m := strings.TrimSpace(b.Status.Method); m != "" {
		return m
	}
	return strings.TrimSpace(b.Spec.Method)
}

// backupsFor filters a listing to one cluster, newest first.
//
// Filtered on `spec.cluster.name` rather than a label selector. CNPG does
// label its Backup objects, but the label is the operator's own bookkeeping
// and the field is the one the CRD declares as a printer column — so this
// filters on the thing the resource is documented to mean rather than on an
// implementation detail that has no compatibility promise.
func backupsFor(items []backupObject, cluster string) []backupObject {
	out := make([]backupObject, 0, len(items))
	for _, b := range items {
		if cluster == "" || b.Spec.Cluster.Name == cluster {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, z := out[i].Metadata.CreationTimestamp, out[j].Metadata.CreationTimestamp
		if !a.Equal(z) {
			return a.After(z)
		}
		return out[i].Metadata.Name < out[j].Metadata.Name
	})
	return out
}

func backupListCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "cnpg.backup.list",
		Summary:    "Backups taken of a cluster: when, how, where they went, and what failed",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "The Backup objects themselves, which the Cluster resource does not " +
			"carry. `cnpg.status` answers 'is this cluster backed up' from three summary " +
			"fields the operator maintains — last success, last failure, first recoverable " +
			"point — and that is the right answer to that question. This is the other one: " +
			"which individual backups exist, how each was taken, how long it took, where " +
			"the bytes went, and what the operator said when one failed.\n\n" +
			"Credentials are not read. A Backup's status carries the object-store " +
			"credential references its cluster was configured with, and this decodes none " +
			"of them — the names of the secrets holding your keys are not part of any " +
			"question a backup listing answers.",
		Run: runBackupList,
	}, plugin.Field{Name: "cluster", Type: plugin.String, Positional: true,
		Help: "only this cluster's backups — every one in the namespace when omitted",
		Live: true, Suggest: suggestClusters})
}

func runBackupList(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	name := strings.TrimSpace(req.String("cluster"))
	if verr := checkName("cluster", name); verr != nil {
		return nil, verr
	}
	var list backupList
	if verr := getResource(ctx, s, backupCRD, "", &list); verr != nil {
		return nil, verr
	}
	rows := backupsFor(list.Items, name)
	if len(rows) == 0 {
		return view.Text{Body: emptyBackupBody(name, s)}, nil
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Backup"}, {Name: "Cluster"}, {Name: "Status", Kind: view.KindStatus},
		{Name: "Phase"}, {Name: "Method"}, {Name: "Took"}, {Name: "Age"}, {Name: "Where"},
	}}
	for _, b := range rows {
		t.Rows = append(t.Rows, []string{
			b.Metadata.Name, b.Spec.Cluster.Name, b.status(), orDash(b.Status.Phase),
			orDash(b.resolvedMethod()), b.took(), age(b.Metadata.CreationTimestamp), b.where(),
		})
	}
	t.Total = len(t.Rows)
	// The error is its own section rather than a ninth column: a barman
	// failure is a paragraph of somebody else's stderr, and a table that has
	// to hold one is a table with eight unreadable columns.
	if problems := backupErrors(rows); len(problems.Rows) > 0 {
		return view.Sections{Items: []view.Section{
			{Title: "Backups", View: t},
			{Title: "What failed", View: problems},
		}}, nil
	}
	return t, nil
}

func backupErrors(rows []backupObject) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "Backup"}, {Name: "Status", Kind: view.KindStatus}, {Name: "Error"},
	}}
	for _, b := range rows {
		if msg := strings.TrimSpace(b.Status.Error); msg != "" {
			t.Rows = append(t.Rows, []string{b.Metadata.Name, "fail", firstLine(msg)})
		}
	}
	t.Total = len(t.Rows)
	return t
}

// emptyBackupBody tells the two empties apart, because they send somebody to
// different places: a cluster nothing has ever backed up is a configuration
// question, and a namespace with no Backup objects at all may simply be one
// where the schedule has not fired yet.
func emptyBackupBody(cluster string, s selection) string {
	if cluster != "" {
		return "No backups of " + cluster + " in " + s.where() + ".\n\n" +
			"`rta cnpg status " + cluster + "` says whether anything is configured to take one."
	}
	return "No CloudNativePG backups in " + s.where() + "."
}

func backupRequestCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:      "cnpg.backup.request",
		Summary: "Ask the operator to back a cluster up now, using that cluster's own configuration",
		Safety:  plugin.Write,
		// Not idempotent, and visibly so: two calls are two backups, each
		// costing a full copy of the database in whatever the cluster pays
		// for storage.
		Idempotent: false,
		// A grant, even though Write already keeps this off the default MCP
		// surface — `--allow-write cnpg` is what puts it there at all. The two
		// gates answer different questions and the operator wants both: the
		// flag is "this plugin may change things on this server", said once at
		// launch, and the grant is "this agent may back up this cluster until
		// this time", said per agent and expiring on its own.
		NeedsGrant: true,
		// The cluster, so a grant to back up one database is not a grant to
		// back up every database the kubeconfig can reach.
		//
		// **A cluster name and not a namespace, with a residual worth stating
		// plainly**: the scope is one input's value, so a grant naming
		// `shop-db` does not distinguish two namespaces that each hold a
		// cluster by that name. That is the same shape and the same tradeoff
		// plugins/kube accepted for `kube.serviceaccount.revoke`, and it is
		// tolerable here for a reason specific to this capability — the
		// backup goes wherever that cluster's own spec already said, which no
		// caller chooses and no caller sees, and the operation removes
		// nothing and reveals nothing. The cost of a confusion is an
		// unexpected backup, not an unexpected disclosure.
		Scope: "cluster",
		Description: "Creates a Backup object. **rta does not take the backup and does not " +
			"choose where it goes** — the operator does both, using the configuration " +
			"already on the cluster: destination, credentials, retention and encryption " +
			"all come from `.spec.backup` or from the WAL-archiver plugin the cluster " +
			"names. A Backup carries a cluster reference and nothing else, which is what " +
			"makes this safe to expose at all: there is no destination for a caller to " +
			"point somewhere useful to them.\n\n" +
			"Refused when the cluster configures no backup at all. CloudNativePG accepts " +
			"such a Backup and lets it fail asynchronously — verified against a running " +
			"operator, which admits the object with no complaint — so the failure would " +
			"surface minutes later in a place nobody is looking. rta reads the cluster " +
			"first and says so instead.\n\n" +
			"`--method`, `--target` and `--online` override what the cluster settled on, " +
			"and are all optional: sending none of them is the ordinary call and means " +
			"'do what you would have done anyway'.",
		Run: runBackupRequest,
	},
		plugin.Field{Name: "cluster", Type: plugin.String, Positional: true, Required: true,
			Help: "the cluster to back up",
			Live: true, Suggest: suggestClusters},
		// The three overrides carry the CRD's own enums, so a wrong value is
		// refused by rta with the list in hand rather than by the API server
		// with a schema error.
		plugin.Field{Name: "method", Type: plugin.String,
			Options: []string{"barmanObjectStore", "volumeSnapshot", "plugin"},
			Help:    "how to take it — the cluster's own choice when omitted"},
		plugin.Field{Name: "target", Type: plugin.String,
			Options: []string{"primary", "prefer-standby"},
			Help:    "which instance performs it — the cluster's own choice when omitted"},
		plugin.Field{Name: "online", Type: plugin.String,
			Options: []string{"true", "false"},
			Help: "hot or cold — only with --method volumeSnapshot, and the cluster's " +
				"own choice when omitted"})
}

// backupRequest is the object sent to the API server.
//
// Written as a struct with omitempty rather than a map, so that a field
// nobody asked about is absent from the document rather than present and
// empty. That distinction is the whole contract of this capability: an absent
// `method` means "the cluster decides" and an empty one is a value CNPG has to
// interpret.
type backupRequest struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name      string            `json:"name"`
		Namespace string            `json:"namespace,omitempty"`
		Labels    map[string]string `json:"labels,omitempty"`
	} `json:"metadata"`
	Spec struct {
		Cluster struct {
			Name string `json:"name"`
		} `json:"cluster"`
		Method string `json:"method,omitempty"`
		Target string `json:"target,omitempty"`
		Online *bool  `json:"online,omitempty"`
	} `json:"spec"`
}

// requestedBy labels what rta asked for, so an operator finding an unexpected
// backup can tell where it came from without reading an audit log on another
// machine. It records the tool and nothing about who ran it: rta's ledger is
// where "which agent, under which grant" is answered, and copying an identity
// into a cluster object would put it somewhere with different readers and no
// expiry.
const requestedBy = "app.kubernetes.io/created-by"

func runBackupRequest(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	name := strings.TrimSpace(req.String("cluster"))
	if verr := checkName("cluster", name); verr != nil {
		return nil, verr
	}
	if name == "" {
		return nil, view.Errorf("cnpg.backup.nocluster", "no cluster named").
			WithHint("`rta cnpg list` shows what is there")
	}

	// The cluster is read before anything is written, and it earns its round
	// trip three times over: it proves the cluster exists (a Backup naming a
	// missing cluster is accepted and fails later), it proves something is
	// configured to perform the backup, and it is what the receipt quotes
	// when it says where the bytes are going.
	var c cluster
	if verr := getJSON(ctx, s, name, &c); verr != nil {
		return nil, verr
	}
	if !c.backupConfigured() {
		return nil, view.Errorf("cnpg.backup.unconfigured",
			"%s configures no backup, so nothing would perform this one",
			c.Metadata.Namespace+"/"+c.Metadata.Name).
			WithHint("the cluster needs a `.spec.backup` stanza, or a WAL-archiver entry " +
				"in `.spec.plugins` — CloudNativePG accepts a Backup either way and fails " +
				"it minutes later, which is why rta stops here instead")
	}

	doc, b, verr := buildBackupRequest(req, c, s)
	if verr != nil {
		return nil, verr
	}
	// The second half of the same pre-flight, and it exists because `method`
	// and `target` read alike and behave differently: an unstated target
	// really does defer to the cluster, and an unstated method is
	// barmanObjectStore whatever the cluster prefers. So a cluster set up for
	// volume snapshots and asked for a backup with nothing said gets a
	// barmanObjectStore one it cannot perform — accepted, then failed
	// minutes later, exactly the shape the unconfigured check above stops.
	if !c.canTake(b.Spec.Method) {
		return nil, methodRefusal(c, b.Spec.Method)
	}
	if req.DryRun {
		return dryRunView(b, c, doc), nil
	}

	var created backupObject
	if verr := createJSON(ctx, s, doc, &created); verr != nil {
		return nil, verr
	}
	return requestReceipt(created, b, c), nil
}

// methodRefusal words the mismatch, and has a case for the cluster that
// configures a backup stanza naming no mechanism at all.
//
// That case looks like a hair-split and is not: `.spec.backup` carrying only
// a `retentionPolicy` passes backupConfigured — the stanza is there — and
// leaves configuredMethods empty, so the obvious message ("this cluster
// configures %s, pass --method %s" off the first entry) indexes an empty
// slice and takes the plugin down. Found by a fixture that turned out to be
// shaped exactly like it.
func methodRefusal(c cluster, asked string) *view.Error {
	named := asked
	if named == "" {
		named = defaultBackupMethod + ", CloudNativePG's default for a Backup that says nothing"
	}
	verr := view.Errorf("cnpg.backup.method.unconfigured",
		"%s is not set up to take a %s backup",
		c.Metadata.Namespace+"/"+c.Metadata.Name, named)
	have := c.configuredMethods()
	if len(have) == 0 {
		return verr.WithHint("its `.spec.backup` names no mechanism — it needs a " +
			"`barmanObjectStore` or a `volumeSnapshot` stanza under it before anything " +
			"can take a backup")
	}
	return verr.WithHint("this cluster configures " + strings.Join(have, " and ") +
		" — pass `--method " + have[0] + "`")
}

// buildBackupRequest assembles the document, and validates every value that
// goes into it before any of it is encoded.
func buildBackupRequest(req plugin.Request, c cluster, s selection) ([]byte, backupRequest, *view.Error) {
	var b backupRequest
	b.APIVersion, b.Kind = "postgresql.cnpg.io/v1", "Backup"
	// Named from the cluster and the minute, because a Backup's name is how
	// somebody finds it again and `backup-8f3a` is not that. Seconds are in
	// it so two requests a minute apart do not collide, which is the ordinary
	// spacing when the first one is retried.
	b.Metadata.Name = c.Metadata.Name + "-rta-" + time.Now().UTC().Format("20060102150405")
	// The namespace the cluster was actually read from, never the flag. They
	// agree whenever the flag was given, and when it was not this is the
	// context's own — which is the namespace the pre-flight read resolved, so
	// the object lands beside the cluster it names rather than wherever a
	// second resolution would have put it.
	b.Metadata.Namespace = c.Metadata.Namespace
	b.Metadata.Labels = map[string]string{requestedBy: "rta"}
	b.Spec.Cluster.Name = c.Metadata.Name

	method := strings.TrimSpace(req.String("method"))
	if verr := oneOf("method", method, "barmanObjectStore", "volumeSnapshot", "plugin"); verr != nil {
		return nil, b, verr
	}
	b.Spec.Method = method

	target := strings.TrimSpace(req.String("target"))
	if verr := oneOf("target", target, "primary", "prefer-standby"); verr != nil {
		return nil, b, verr
	}
	b.Spec.Target = target

	// `online` is a string of "true"/"false" rather than a Bool, because a
	// Bool has no way to say nothing: it arrives as false, and false is a
	// statement — "take this one cold" — that would silently override a
	// cluster configured for hot backups on every call that never mentioned
	// it. The three-state answer is the honest one, so the field carries it.
	switch online := strings.TrimSpace(req.String("online")); online {
	case "":
	case "true", "false":
		// **Only with an explicit volumeSnapshot method, which the CRD schema
		// does not say and a live operator does.** CNPG's webhook refuses
		// `spec.online` on any other method — "Online parameter can be
		// specified only if the backup method is volumeSnapshot" — and
		// `method` carries a schema default of barmanObjectStore, so leaving
		// it out is choosing the method that forbids this rather than leaving
		// the question open. Found by putting the document rta builds through
		// a real API server; the schema alone admits it happily.
		if method != "volumeSnapshot" {
			return nil, b, view.Errorf("cnpg.backup.online.unavailable",
				"online only means something for a volumeSnapshot backup").
				WithHint("pass `--method volumeSnapshot` with it, or leave online out and " +
					"let the cluster's own `.spec.backup.volumeSnapshot.online` decide")
		}
		v := online == "true"
		b.Spec.Online = &v
	default:
		return nil, b, view.Errorf("cnpg.backup.online.invalid",
			"online is %q, which is neither true nor false", online)
	}

	doc, err := json.Marshal(b)
	if err != nil {
		return nil, b, view.Errorf("cnpg.backup.encode",
			"could not build the Backup object: %v", err)
	}
	return doc, b, nil
}

// oneOf refuses a value the CRD's own enum does not admit.
//
// Refused here as well as offered as Options, because Options is a
// suggestion the CLI and TUI render and not a constraint every surface
// enforces — and the alternative to refusing is the API server rejecting the
// document with a schema error that names a JSON path rather than a flag.
func oneOf(field, value string, allowed ...string) *view.Error {
	if value == "" {
		return nil
	}
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return view.Errorf("cnpg.backup."+field+".invalid",
		"%s is %q", field, value).
		WithHint("CloudNativePG admits " + strings.Join(allowed, ", ") +
			" — leaving it out means whatever the cluster already decided")
}

// dryRunView shows the exact document, because "what would this send" is the
// only question a dry run of a create is asked.
func dryRunView(b backupRequest, c cluster, doc []byte) view.View {
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "would create", Value: b.Metadata.Namespace + "/" + b.Metadata.Name},
		{Key: "for cluster", Value: c.Metadata.Namespace + "/" + c.Metadata.Name},
		{Key: "how", Value: chosenBy(b.Spec.Method, "method")},
		{Key: "where", Value: destinationOf(c)},
		{Key: "which instance", Value: chosenBy(b.Spec.Target, "target")},
		{Key: "the object", Value: string(doc)},
		{Key: "nothing was sent", Value: "this is a dry run — the cluster was read and not written"},
	}}
}

// requestReceipt says what was asked for, what will perform it, and where to
// look next. It never claims the backup happened: creating the object is the
// whole of what rta did, and the operator's work starts afterwards.
func requestReceipt(created backupObject, b backupRequest, c cluster) view.View {
	name := created.Metadata.Name
	if name == "" {
		name = b.Metadata.Name
	}
	ns := created.Metadata.Namespace
	if ns == "" {
		ns = b.Metadata.Namespace
	}
	pairs := []view.Pair{
		{Key: "requested", Value: ns + "/" + name},
		{Key: "cluster", Value: c.Metadata.Namespace + "/" + c.Metadata.Name},
		{Key: "how", Value: chosenBy(b.Spec.Method, "method")},
		{Key: "where", Value: destinationOf(c)},
		{Key: "which instance", Value: chosenBy(b.Spec.Target, "target")},
	}
	if p := strings.TrimSpace(created.Status.Phase); p != "" {
		pairs = append(pairs, view.Pair{Key: "phase", Value: p})
	}
	return view.KeyValue{Pairs: append(pairs,
		view.Pair{Key: "rta did not take it", Value: "the object is a request; CloudNativePG " +
			"performs the backup, and a Backup that was accepted can still fail"},
		view.Pair{Key: "watch it", Value: "`rta cnpg backup list " + c.Metadata.Name + "`"},
	)}
}

// chosenBy words an empty override as the thing it means, and the two fields
// mean different things.
//
// `target` empty really is "the cluster decides": the CRD says it defers to
// `cluster.spec.backup.target`. `method` empty is barmanObjectStore, a fixed
// default that has nothing to do with what the cluster prefers — so the row
// names the method that will be used and says who picked it, rather than
// claiming a deferral that does not happen.
func chosenBy(value, what string) string {
	if value != "" {
		return value + " (stated for this backup)"
	}
	if what == "method" {
		return defaultBackupMethod + " — CloudNativePG's default, not a preference read " +
			"off the cluster; rta stated none"
	}
	return "whatever the cluster's own " + what + " says — rta stated none"
}

// destinationOf says where a backup of this cluster goes, as far as the
// Cluster resource itself admits.
//
// Deliberately unspecific about the object store's address. The endpoint and
// bucket sit inside `.spec.backup.barmanObjectStore` beside the credential
// references, and this plugin decodes neither — naming which mechanism carries
// the bytes is what the receipt needs, and reading the rest to print a URL
// would be decoding a credential stanza to render one field of it.
func destinationOf(c cluster) string {
	switch {
	case c.walArchiverPlugin() != "":
		return "wherever the " + c.walArchiverPlugin() + " plugin this cluster names sends it"
	case c.Spec.Backup != nil:
		return "wherever this cluster's `.spec.backup` sends it"
	}
	return "—"
}
