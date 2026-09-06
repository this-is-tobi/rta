// Package pluginhost launches external plugin processes and makes them
// indistinguishable from built-ins.
//
// What comes back from Open is a plugin.Plugin: the same value builtin/fs
// returns, with the same capabilities carrying the same handler signatures.
// Every surface — CLI, TUI, MCP, JSON — is already written against that type
// and needs no notion of "remote". That is the whole architectural bet, and
// it is why the wire package converts declarations rather than the renderers
// learning a second shape.
//
// The security decisions here are confinement's, and they are decisions this
// package makes alone. There is no per-call launch option beyond argv,
// environment and standard descriptors, and no message in proto/v1 for a
// plugin to ask for anything different. That absence is deliberate: a plugin
// that could negotiate its own confinement is a plugin that will.
package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk"
	"github.com/this-is-tobi/rta/pkg/sdk/wire"
	"github.com/this-is-tobi/rta/pkg/view"
	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
)

// Timeouts, both explicit, because both defaults are wrong for a CLI.
//
// go-plugin's StartTimeout defaults to one minute. That is the time rta waits
// for a subprocess to print one line of handshake on a local machine, and it
// is paid on every single invocation by any executable on $PATH that happens
// to match the plugin prefix and is not one. Measured before this constant
// existed: a shell script named rta-plugin-x that merely slept made `rta
// plugins` hang for a full minute, and orphaned the sleep on the way out.
//
// Five seconds is generous for fork, exec and one write. It is deliberately
// not tighter: a binary being scanned by Gatekeeper on its first run after
// download, or read off a cold network filesystem, is slow once and fast
// afterwards, and failing that case would look like a broken plugin rather
// than a slow disk.
//
// Describe needs its own bound because the handshake succeeding says nothing
// about the RPC answering — a plugin can complete the handshake and then
// never reply, and without this that is an indefinite hang with the process
// cache locked behind it.
// Variables rather than constants for one reason, and it is not
// configuration: a race-instrumented build raises them (see slow_race.go).
// Nothing else writes to them, and there is no flag or environment variable
// behind them — the values below are what every real invocation uses.
var (
	startTimeout    = 5 * time.Second
	describeTimeout = 5 * time.Second
)

// killTimeout bounds go-plugin's own shutdown before rta stops waiting for it
// and takes the process group instead. Its internal graceful wait is two
// seconds, so this is the smallest value that does not routinely pre-empt a
// shutdown that was about to succeed.
const killTimeout = 3 * time.Second

// Host owns every running plugin process.
//
// Processes are cached by (binary digest, confinement spec hash) and reused:
// the default dashboard is one tile per plugin refreshing on a timer, so a
// spawn per call would mean a process launch every few seconds per plugin. A
// call whose spec hash differs from a running process's gets a new process
// rather than being served by the old one — see specHash for why that rule is
// encoded now despite being unable to fire yet.
//
// There is no LRU, no idle reaper and no process cap. The confinement profile
// is per-plugin and constant, so there is nothing to evict on, and two
// unmeasured constants would recreate exactly the respawn latency the cache
// exists to avoid.
type Host struct {
	mu      sync.Mutex
	running map[string]*Client
	// untrusted is what discovery found and refused to launch, in $PATH
	// order. Kept rather than only reported, so `rta plugin list` and `rta
	// doctor` can show an operator the plugin that is installed and silent —
	// which is the whole failure mode a trust gate introduces, and the one it
	// has to answer for.
	untrusted []Untrusted
	// Stderr receives plugin stderr. Nil means discard.
	Stderr io.Writer
}

// Untrusted is a discovered plugin binary that nothing has approved, and that
// therefore was never executed. Everything here comes from the filesystem: the
// name it is installed under and the hash of its bytes. Nothing it says about
// itself is in here, because saying anything about itself would have required
// running it.
type Untrusted struct {
	// Taken is set when something already answers to this name — a built-in,
	// or a trusted plugin that got there first. Such an artifact is not a
	// missing capability waiting on an approval: approving it would only earn
	// a namespace collision on the next start, so the surfaces that report it
	// must not offer `rta plugin trust` as the fix.
	Taken  bool
	Name   string
	Path   string
	Digest string
}

// Short is the digest as a person quotes it.
func (u Untrusted) Short() string {
	if len(u.Digest) > 12 {
		return u.Digest[:12]
	}
	return u.Digest
}

// remember records a plugin that was found and not run.
func (h *Host) remember(u Untrusted) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.untrusted = append(h.untrusted, u)
}

// Untrusted lists what discovery refused to launch.
func (h *Host) Untrusted() []Untrusted {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Untrusted(nil), h.untrusted...)
}

// New builds a host. The zero Host works; New exists for the Stderr option.
func New(stderr io.Writer) *Host {
	return &Host{running: map[string]*Client{}, Stderr: stderr}
}

// Client is one running plugin process.
type Client struct {
	Identity Identity
	Declared plugin.Plugin
	// Unknown lists declaration elements this host could not interpret,
	// keyed by capability ID. A plugin built against a newer proto is not an
	// error — that is what forward compatibility is for — but it is also not
	// nothing, and the caller decides.
	Unknown map[string][]string

	// mu guards the fields below, which revive replaces in place. The
	// capabilities handed to the registry close over this *Client, so
	// swapping its transport underneath them is what lets a restarted plugin
	// keep serving handlers that were registered against the dead one.
	mu     sync.Mutex
	client *goplugin.Client
	stub   rtav1.PluginServiceClient
	cmd    *exec.Cmd
	logger hclog.Logger

	// raw is the declaration exactly as it arrived, kept because attach needs
	// the has_prefill/has_suggest flags that do not survive into
	// plugin.Capability, and because it is what gets cached.
	raw *rtav1.Plugin

	// What it takes to launch this again, kept so revive does not have to be
	// handed the world.
	host *Host
	deny DenySet
	args []string
}

// live returns the stub to call, restarting the process first if it has died.
//
// The alternative was a lie in a hint. A crashed plugin left every capability
// the registry had already indexed pointing at a dead stub, forever, while
// the error told the user "it will be started again on the next call" —
// because nothing was going to. That barely shows on the CLI, where the
// process lives for one command, and it is the whole experience in the TUI,
// where a plugin that crashed once has a dead tile until rta is restarted.
//
// What it deliberately does NOT do is retry the call that failed. A plugin
// that died mid-write may or may not have written; re-sending is how one
// crash becomes two applications of a non-idempotent operation. The failed
// call reports honestly and the *next* one gets a live process, which is what
// the hint always claimed.
func (c *Client) live(ctx context.Context) (rtav1.PluginServiceClient, error) {
	// Registered before the unlock and therefore run after it: defers are
	// LIFO, so this is how the process being replaced gets torn down outside
	// the lock it was read under. Synchronous rather than a goroutine so the
	// restart is observable — a test can assert the old connection is gone
	// rather than eventually gone — and it is bounded by teardown's own
	// killTimeout, which is the wait Close already accepts.
	var (
		old       *goplugin.Client
		oldCmd    *exec.Cmd
		oldLogger hclog.Logger
	)
	defer func() {
		if old != nil {
			teardown(old, oldCmd, oldLogger)
		}
	}()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil && !c.client.Exited() {
		return c.stub, nil
	}
	if c.host == nil {
		return c.stub, nil // not host-managed (tests build Clients directly)
	}

	// Re-identify from the recorded path. A binary that changed on disk is a
	// different artifact, and the declaration rta already published to every
	// surface came from the old one — so silently serving the new one would
	// mean the catalogue and the process disagree about what exists, which is
	// the one thing this package validates against at startup.
	id, err := Identify(c.Identity.Path)
	if err != nil {
		return nil, fmt.Errorf("restarting plugin %s: %w", c.Identity.Path, err)
	}
	if id.Digest != c.Identity.Digest {
		return nil, fmt.Errorf(
			"the plugin at %s changed on disk (%s → %s); restart rta so its capabilities are re-read",
			c.Identity.Path, c.Identity.Short(), id.Short())
	}

	fresh, err := c.host.launch(ctx, id, c.deny, c.args)
	if err != nil {
		return nil, fmt.Errorf("restarting plugin %s: %w", c.Identity.Path, err)
	}
	// Same bytes, so Describe is the same answer; asserting it costs nothing
	// and turns a wrong assumption into a message instead of a mystery.
	if len(fresh.Declared.Capabilities) != len(c.Declared.Capabilities) {
		fresh.Close()
		return nil, fmt.Errorf("the plugin at %s declared a different catalogue on restart", c.Identity.Path)
	}
	old, oldCmd, oldLogger = c.client, c.cmd, c.logger
	c.client, c.stub, c.cmd, c.logger = fresh.client, fresh.stub, fresh.cmd, fresh.logger
	return c.stub, nil
}

// usable reports whether this client can be handed to a caller: either it has
// no process yet — the ordinary state once declarations come from the cache —
// or the one it has is still alive.
//
// It exists so the two places that ask are not reading c.client while live()
// replaces it. Both held h.mu when they asked, which is a different lock from
// the one that guards the field, and a different lock is no lock.
//
// Lock order is h.mu then c.mu wherever both are held. Nothing takes them the
// other way round: live() holds c.mu and calls launch, which takes neither.
func (c *Client) usable() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client == nil || !c.client.Exited()
}

// Close kills the process and everything it started.
func (c *Client) Close() {
	if c == nil {
		return
	}
	// Silence first. Tearing a plugin down means the socket goes away under
	// whatever is still writing to it, and go-plugin's yamux session logs
	// that as an error — "failed to write header: broken pipe". It was on
	// stderr for roughly two invocations in five, which is a defect report
	// printed at a user for something rta did on purpose. Nothing after this
	// point can report a fault worth acting on, because from here the fault
	// is the intended outcome.
	// Snapshotted under the lock, because live() replaces all four of these
	// in place — that is what lets a restarted plugin keep serving handlers
	// registered against the dead one — and Close read them without holding
	// it. Concurrent shutdown-while-restarting was therefore an unsynchronised
	// read of exactly the fields being written.
	//
	// Not held across the teardown itself: Kill and reap can take seconds,
	// and holding c.mu through them would block every in-flight call's live()
	// on a process that is going away regardless.
	c.mu.Lock()
	client, cmd, logger := c.client, c.cmd, c.logger
	c.mu.Unlock()

	teardown(client, cmd, logger)
}

// teardown ends one plugin process and everything it started.
//
// Shared by Close and by live, which discards a process every time it
// restarts one. live used to drop it on the floor: the four fields were
// replaced in place and the old *goplugin.Client was never killed, so its
// gRPC ClientConn was never closed. Measured against the example plugin,
// that is four goroutines per restart — grpcsync callback serializers, an
// HTTP/2 client transport and a yamux session, all serving a dead process —
// which on the CLI costs nothing and in `rta mcp serve` accumulates on
// exactly the path live() exists for.
//
// go-plugin's Kill first, so a well-behaved plugin gets its graceful
// shutdown; then the group, which catches whatever it spawned and did not
// clean up. Doing only the first leaves orphans holding the terminal; doing
// only the second denies every plugin a clean exit.
//
// But Kill is bounded, because it can block indefinitely on exactly the case
// the reap exists for. Its deferred clientWaitGroup.Wait() waits on the
// goroutines reading the child's stdout and stderr, and those pipes stay
// open as long as *any* process holds them — including a grandchild
// go-plugin never knew about, since runner.Kill only signals the direct
// child. So a plugin that forks and exits leaves Kill waiting for the
// grandchild's full lifetime, with the reap that would free it sitting on
// the next line, unreachable. Measured: a plugin whose whole body was
// `sleep 300` hung rta for the full five minutes.
func teardown(client *goplugin.Client, cmd *exec.Cmd, logger hclog.Logger) {
	if logger != nil {
		logger.SetLevel(hclog.Off)
	}
	killed := make(chan struct{})
	go func() {
		defer close(killed)
		if client != nil {
			client.Kill()
		}
	}()
	select {
	case <-killed:
		// Clean shutdown. Still reap: a graceful exit says the plugin
		// stopped, not that everything it started did.
		reap(cmd)
	case <-time.After(killTimeout):
		// Break the pipes Kill is waiting on, which lets it finish.
		reap(cmd)
		select {
		case <-killed:
		case <-time.After(killTimeout):
			// Nothing left to try, and blocking here would trade a leaked
			// goroutine for a hung command. The process group has had a
			// SIGKILL; rta exiting is what collects the rest.
		}
	}
}

// Open launches a plugin by command name, or returns the running one.
func (h *Host) Open(ctx context.Context, name string, args ...string) (*Client, error) {
	if err := available(); err != nil {
		return nil, err
	}
	deny, err := Resolve()
	if err != nil {
		return nil, err
	}
	id, err := Identify(name)
	if err != nil {
		return nil, err
	}
	return h.openIdentified(ctx, id, deny, args)
}

// openIdentified is Open for a caller that has already hashed the artifact.
//
// Split out so that LoadInto can check the digest against what the operator
// trusts and then launch *that* digest, rather than hashing once to decide and
// again to run. Two hashes would be two reads of a file that can change
// between them — a window this does not need to open, on the one path whose
// whole purpose is to decide whether a stranger's code may execute.
// OpenIdentified is Open for a caller outside this package that has already
// hashed the artifact and made a decision about that digest.
//
// It exists for the same reason the unexported one does: the decision and the
// launch must name one Identity. Re-resolving by path here would reopen the
// window between them, which is the whole point of the split.
func (h *Host) OpenIdentified(ctx context.Context, id Identity, args ...string) (*Client, error) {
	if err := available(); err != nil {
		return nil, err
	}
	deny, err := Resolve()
	if err != nil {
		return nil, err
	}
	return h.openIdentified(ctx, id, deny, args)
}

// OpenAllowing is Open for a caller that has decided this artifact may read
// the credential locations it declares.
//
// **`rta plugin dev` is the caller, and the reason is the one that already
// exempts it from trust**: compiling from a directory named in the command
// just typed is a stronger act of approval than a digest in a file. Without
// it a plugin author cannot exercise their own declaration at all — the
// temporary binary is rebuilt on every run, so its digest is new every time
// and `rta plugin allow` could never name it. The mechanism would be usable
// by everybody except the people writing for it.
//
// Nothing else calls this. An installed plugin's grant is read from the trust
// record by digest, which is the path that has an operator's decision behind
// it.
func (h *Host) OpenAllowing(ctx context.Context, name string, allowed []plugin.Need, args ...string) (*Client, error) {
	if err := available(); err != nil {
		return nil, err
	}
	deny, err := ResolveAllowing(allowed)
	if err != nil {
		return nil, err
	}
	id, err := Identify(name)
	if err != nil {
		return nil, err
	}
	return h.openIdentified(ctx, id, deny, args)
}

func (h *Host) openIdentified(ctx context.Context, id Identity, deny DenySet, args []string) (*Client, error) {
	// Here and not in buildCmd, so the cache key, the launch and the restart
	// (which relaunches under c.deny) all see the same policy.
	deny, err := deny.Launching(id.Path)
	if err != nil {
		return nil, err
	}
	key := cacheKey(id, deny, args)

	if c := h.cached(key); c != nil {
		return c, nil
	}

	// Launched outside the lock. Holding it across a fork, a handshake and an
	// RPC would mean one plugin that is slow to start blocks every other
	// plugin's lookup — and, worse, blocks CloseAll, so the ctrl-c that was
	// meant to escape a hanging startup could not clean up after it either.
	c, err := h.describeOnly(ctx, id, deny, args)
	if err != nil {
		return nil, err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running == nil {
		h.running = map[string]*Client{}
	}
	// Checked again: two goroutines can reach the launch above for the same
	// key. The first to arrive here wins and the loser's process is closed,
	// so the cache never holds a process nothing will use.
	if existing, ok := h.running[key]; ok && existing.usable() {
		c.Close()
		return existing, nil
	}
	h.running[key] = c
	return c, nil
}

// describeOnly returns a Client that knows what the plugin declares, with a
// live process only if it took one to find out.
//
// The cache hit path launches nothing. Client.live starts the process on the
// first call that needs it, which is the same code path a crashed plugin
// takes to come back — so "not started yet" and "died" are one case rather
// than two that drift.
//
// A miss keeps the process it just paid for rather than killing it and
// starting again on the next call: the common shape of a miss is the first
// run after installing a plugin, and that run is usually about to use it.
func (h *Host) describeOnly(ctx context.Context, id Identity, deny DenySet, args []string) (*Client, error) {
	if raw, ok := readCache(id.Digest); ok {
		c := &Client{Identity: id, host: h, deny: deny, args: args}
		if err := c.adopt(raw); err == nil {
			return c, nil
		}
		// A cached declaration this rta will not accept is not a reason to
		// refuse the plugin: the entry may predate a validation rule. Fall
		// through and ask the process, which is also what replaces the entry.
	}
	c, err := h.launch(ctx, id, deny, args)
	if err != nil {
		return nil, err
	}
	writeCache(id.Digest, c.raw)
	return c, nil
}

// cacheKey identifies a process: this binary, under this policy, with these
// arguments.
//
// argv is in the key because argv is one of the three launch levers this
// package exposes, and a lever that changes the process but not
// its cache key hands a caller a process configured for somebody else. It
// cannot fire today — LoadInto passes no arguments — which is exactly why it
// is worth encoding now rather than after `rta plugin dev` starts passing
// some.
func cacheKey(id Identity, deny DenySet, args []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n%d\n", id.Digest, specHash(deny), len(args))
	for _, a := range args {
		fmt.Fprintf(h, "%d:%s\n", len(a), a)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// cached returns a usable client for key, or nil.
//
// A nil client field means "declared, not started yet", which is the ordinary
// state once declarations come from the cache — and it is usable, because the
// first call starts the process. Requiring a live process here was the first
// version and it quietly disabled the whole process cache: every Open built a
// fresh Client, so two callers of the same plugin got two processes.
func (h *Host) cached(key string) *Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.running[key]; ok && c.usable() {
		return c
	}
	return nil
}

// CloseAll kills every process this host started.
func (h *Host) CloseAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, c := range h.running {
		c.Close()
		delete(h.running, key)
	}
}

// buildCmd assembles the exact command a plugin is launched as.
//
// Separate from launch so the spawn can be inspected without spawning it.
// Everything that makes this command safe — the sandbox wrapper, the
// environment allowlist, the process group — is applied here, and a test that
// asserted about a ClientConfig it cannot read back would be asserting about
// its own copy of the code rather than about what runs.
func buildCmd(id Identity, deny DenySet, args []string) *exec.Cmd {
	name, argv := wrap(deny, id.Path, args)
	cmd := exec.Command(name, argv...)
	// Not exec.CommandContext: the process outlives one call by design, and
	// binding its lifetime to the ctx of whichever call happened to spawn it
	// would kill it the moment that call returned.
	cmd.Env = Environ()
	harden(cmd)
	return cmd
}

func (h *Host) launch(ctx context.Context, id Identity, deny DenySet, args []string) (*Client, error) {
	cmd := buildCmd(id, deny, args)

	logger := hclog.New(&hclog.LoggerOptions{
		Name:       "plugin." + id.Short(),
		Output:     h.stderr(),
		Level:      hclog.Error,
		JSONFormat: true,
	})
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: sdk.Handshake,
		Plugins:         goplugin.PluginSet{sdk.PluginSetName: noDispense{}},
		Cmd:             cmd,
		AllowedProtocols: []goplugin.Protocol{
			goplugin.ProtocolGRPC, // and only gRPC: net/rpc is not a protocol rta speaks
		},
		// The host's environment is not the plugin's. env.go says which
		// names cross and why each one is there.
		SkipHostEnv: true,
		// mTLS on the loopback socket. This was listed among the things
		// go-plugin gives us for free; it is off by default and dialGRPCConn
		// takes the insecure branch without it.
		AutoMTLS: true,
		// Puts the plugin socket in a private 0700 directory instead of bare
		// $TMPDIR at mode srwxr-xr-x.
		GRPCBrokerMultiplex: true,
		// See the constant: the default is a minute, paid on every rta
		// invocation by anything on $PATH that matches the prefix and is not
		// a plugin.
		StartTimeout: startTimeout,
		// Set explicitly, and this is the field that matters rather than the
		// one an implementer reaches for. Stderr is the obvious-looking knob
		// and is not the leak: Logger defaults to an hclog writing to
		// os.Stderr, logStderr passes each plugin stderr line as the log
		// *message*, and hclog writes a message body unquoted — so a plugin's
		// log.Printf put raw OSC 52 on the host's terminal, reproduced
		// against v1.8.0. Routing it through a JSON logger makes the control
		// bytes data.
		Logger: logger,
	})

	// abandon tears down a process that never became usable.
	//
	// The reap comes FIRST here, the opposite order to Close, and the reason
	// is that there is nothing to preserve: a plugin that failed to hand over
	// an address has no graceful shutdown to attempt, and Kill would block on
	// pipes its own children are holding. Taking the group first closes those
	// pipes, so the Kill that follows returns immediately and go-plugin still
	// gets to clean up its socket directory.
	abandon := func(reason string, err error) (*Client, error) {
		logger.SetLevel(hclog.Off)
		reap(cmd)
		client.Kill()
		return nil, fmt.Errorf("%s %s: %w", reason, id.Path, err)
	}

	if _, err := client.Start(); err != nil {
		return abandon("starting plugin", err)
	}
	proto, err := client.Client()
	if err != nil {
		return abandon("connecting to plugin", err)
	}
	grpcClient, ok := proto.(*goplugin.GRPCClient)
	if !ok {
		return abandon("plugin", fmt.Errorf("did not speak gRPC"))
	}

	c := &Client{
		Identity: id, client: client, cmd: cmd, logger: logger,
		host: h, deny: deny, args: args,
		stub: rtav1.NewPluginServiceClient(grpcClient.Conn),
	}
	if err := c.describe(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (h *Host) stderr() io.Writer {
	if h.Stderr == nil {
		return io.Discard
	}
	return h.Stderr
}

// describe fetches the declaration and attaches the handlers that call back
// over the wire, then validates the result exactly as the built-in registry
// validates a built-in.
//
// Validating here rather than trusting the plugin is the point. Everything in
// that declaration is attacker-controlled if the plugin is hostile and
// merely wrong if it is buggy, and both arrive as the same bytes — a summary
// with an OSC escape in it, a field with a type this host does not know, an
// ID that collides with a built-in. plugin.Validate is where rta already
// decides what a legal declaration is, so a remote one goes through the same
// door rather than a second one that will drift.
func (c *Client) describe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, describeTimeout)
	defer cancel()
	resp, err := c.stub.Describe(ctx, &rtav1.DescribeRequest{})
	if err != nil {
		return fmt.Errorf("describing plugin %s: %w", c.Identity.Path, err)
	}
	return c.adopt(resp.GetPlugin())
}

// adopt turns a declaration into working capabilities.
//
// Split out from describe so that the disk cache and a live Describe go
// through exactly the same decode, attach and validate — the alternative
// being two paths that agree until one of them is changed.
func (c *Client) adopt(raw *rtav1.Plugin) error {
	decl, unknown := wire.PluginFromProto(raw)
	c.Unknown = unknown

	caps := raw.GetCapabilities()
	if len(caps) != len(decl.Capabilities) {
		// Cannot happen through wire.PluginFromProto, which maps one to one.
		// Checked anyway because the two are indexed together below and this
		// is attacker-supplied data: a mismatch would be an index panic in
		// the host, taking down every other plugin with it.
		return fmt.Errorf("plugin %s declared %d capabilities that decoded to %d",
			c.Identity.Path, len(caps), len(decl.Capabilities))
	}
	for i := range decl.Capabilities {
		c.attach(&decl.Capabilities[i], caps[i])
	}
	if err := decl.Validate(); err != nil {
		return fmt.Errorf("plugin %s declared something rta cannot accept: %w", c.Identity.Path, err)
	}
	c.Declared = decl
	c.raw = raw
	return nil
}

// attach wires one capability's handlers to the connection.
func (c *Client) attach(dst *plugin.Capability, raw *rtav1.Capability) {
	id := dst.ID
	dst.Run = func(ctx context.Context, req plugin.Request) (view.View, error) {
		return c.call(ctx, id, req)
	}
	if raw.GetHasPrefill() {
		dst.Prefill = func(ctx context.Context, req plugin.Request) (map[string]any, error) {
			return c.prefill(ctx, id, req)
		}
	}
	// Bounds-checked rather than trusted to line up. wire.PluginFromProto
	// maps inputs one to one so it always does, which is exactly what the
	// capability-count guard in adopt says about capabilities — and this loop
	// indexes attacker-supplied data with a length nothing had checked.
	inputs := raw.GetInputs()
	for i := range dst.Inputs {
		if i >= len(inputs) || !inputs[i].GetHasSuggest() {
			continue
		}
		field := dst.Inputs[i].Name
		dst.Inputs[i].Suggest = func(ctx context.Context, req plugin.Request) []string {
			return c.suggest(ctx, id, field, req)
		}
	}
}
