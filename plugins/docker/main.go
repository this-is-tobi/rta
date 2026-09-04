// Command rta-plugin-docker is the janitorial fast path onto a Docker
// daemon: what is running, what images are taking up the disk, one composed
// overview — and the small, bounded set of mutations a developer runs by hand
// every day.
//
// Build it and put it on your $PATH as `rta-plugin-docker`:
//
//	cd plugins/docker && go build -o ~/.local/bin/rta-plugin-docker .
//
// It needs the `docker` CLI and nothing else. Whatever daemon docker can
// already reach — Docker Desktop's socket, a rootless one, a remote context
// over SSH — this can reach, with no address to configure. See dockercli.go
// for why driving the CLI is a decision rather than a shortcut.
//
// # Why this one is not read-only, when kube is
//
// A deliberate divergence, decided rather than defaulted into.
// `container.stop`, `restart` and `rm` are bounded, single-purpose, and the
// literal loop a developer runs by hand — unlike `docker run`/`build`/
// `compose`, whose unbounded flag surface the native CLI already owns well.
// That is the same line `git` holds when it declines to grow a `git.commit`,
// drawn in a different place because the operations are a different shape.
//
// Three capabilities are **deliberately absent**: `image.prune`,
// `volume.prune` and `network.prune`. The Engine API has no server-side
// dry-run for prune, so a correct preview means reproducing dockerd's own
// filter-matching client-side, and that reproduction drifts across Engine
// versions. A dry-run that can be wrong is worse than none, because its whole
// job is to be believed. They ship when a cross-Engine conformance test
// proves the reproduction does not drift — a real gate, not a formality.
//
// Every capability here reaches off this process, so none appears on the
// automatic dashboard on its own; add one explicitly once you have decided
// polling it is fine:
//
//	dashboard:
//	  tiles:
//	    - id: docker.overview
package main

import (
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk"
)

func main() { sdk.Serve(Plugin()) }

// connFields is the connection half of every capability's inputs.
//
// Both are Local, and for the reason plugins/kube states at length for its
// own `context`: **choosing which daemon a call reaches is choosing a
// destination, and a remote caller may never choose one** (Field.Local,
// a destination is a destination). `--host=tcp://somewhere-else:2375` from an agent would
// aim a stop or an rm at a machine the operator never named. Config fills
// them, a person at a terminal passes them, MCP cannot.
func connFields() []plugin.Field {
	return []plugin.Field{
		{Name: "host", Type: plugin.String, Config: "host", Local: true,
			Help: "daemon address, e.g. unix:///var/run/docker.sock — the CLI's own default when omitted"},
		{Name: "context", Type: plugin.String, Config: "context", Local: true,
			Help: "docker context to use — the current one when omitted"},
	}
}

// cap appends the connection inputs and marks the capability off-dashboard,
// the way plugins/pg, plugins/vault and plugins/kube each do for their own.
func cap(c plugin.Capability, own ...plugin.Field) plugin.Capability {
	c.Inputs = append(append(own, c.Inputs...), connFields()...)
	c.NoPreview = true
	return c
}

// nameInput is the container a mutation acts on: required, positional, and
// the grant's Scope, so a grant reads `docker.container.rm my-scratch-box`
// and authorizes exactly that container.
func nameInput(help string) plugin.Field {
	return plugin.Field{
		Name: "container", Type: plugin.String, Positional: true, Required: true,
		Help: help, Suggest: suggestContainers,
	}
}

// version is what this build claims to be, stamped by whatever built it:
// `-X main.version=`, which is the Makefile's flag and GoReleaser's own
// default. A build nobody stamped says "dev" rather than claiming a release
// number that was never cut — an index entry carries this verbatim, and a
// version is a fact about a release, not about the source it came from.
var version = "dev"

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "docker",
		Summary: "Containers and images: what is running, what is stale, and the daily tidy-up",
		Version: version,
		Capabilities: []plugin.Capability{
			cap(plugin.Capability{
				ID:      "docker.container.list",
				Summary: "Containers, with state, health, ports and age",
				Description: "Running containers by default; --all includes the stopped ones, which " +
					"is usually what somebody wants before a tidy-up. Health is shown separately " +
					"from state because a container can be up and failing its own healthcheck.",
				Safety:     plugin.Read,
				Idempotent: true,
				Run:        runContainerList,
			}, plugin.Field{Name: "all", Type: plugin.Bool,
				Help: "include stopped containers"}),
			cap(plugin.Capability{
				ID:      "docker.image.list",
				Summary: "Images, with size and age",
				Description: "What is on this machine's disk. Dangling images — the untagged " +
					"leftovers of a rebuild — are marked, since they are usually the answer to " +
					"\"where did my disk go\".",
				Safety:     plugin.Read,
				Idempotent: true,
				Run:        runImageList,
			}),
			cap(plugin.Capability{
				ID:      "docker.overview",
				Summary: "One daemon at a glance: what is running, what is unhealthy, what disk is used",
				Description: "Whether the daemon answers, how many containers are up against how " +
					"many exist, anything unhealthy or recently exited, and how much disk images " +
					"are taking. With --detail: the containers and the largest images themselves.",
				Safety:     plugin.Read,
				Idempotent: true,
				Detailed:   true,
				Run:        runOverview,
			}),
			cap(plugin.Capability{
				ID:      "docker.container.inspect",
				Summary: "Everything the daemon knows about one container",
				Description: "Image, command, state, restart policy, mounts, networks and " +
					"environment. **Write rather than Read, and it needs a grant**, because a " +
					"container's environment carries plaintext credentials by convention — every " +
					"`-e` and every compose-file value — and deciding which of those are secret by " +
					"their names is a guess, not a rule. rta would rather ask than guess wrong " +
					"once.",
				// The kv.get precedent: a capability that reveals a secret's
				// plaintext is Write even with no disk side effect at all.
				// Env is why. Unlike net.info's masking, which
				// keys off syntactically certain fields, name-pattern
				// redaction of free-form environment variables is a heuristic
				// — so the gate is the grant rather than a filter that will
				// miss DB_DSN one day.
				Safety:     plugin.Write,
				Idempotent: true,
				NeedsGrant: true,
				Scope:      "container",
				Inputs:     []plugin.Field{nameInput("the container to inspect")},
				Run:        runInspect,
			}),
			cap(plugin.Capability{
				ID:      "docker.container.stop",
				Summary: "Stop a running container",
				Description: "Sends SIGTERM and gives the container time to exit before the daemon " +
					"kills it. Reversible — `docker start` brings it back with the same id, disk " +
					"and configuration — which is why this is Write and not Destructive.",
				Safety:     plugin.Write,
				Idempotent: true,
				NeedsGrant: true,
				Scope:      "container",
				Inputs:     []plugin.Field{nameInput("the container to stop")},
				Run:        runStop,
			}),
			cap(plugin.Capability{
				ID:      "docker.container.restart",
				Summary: "Restart a container",
				Description: "Stop then start, keeping the container's id, volumes and " +
					"configuration. What it does lose is whatever was only in the process's memory " +
					"and whatever was written outside a volume.",
				Safety:     plugin.Write,
				Idempotent: true,
				NeedsGrant: true,
				Scope:      "container",
				Inputs:     []plugin.Field{nameInput("the container to restart")},
				Run:        runRestart,
			}),
			cap(plugin.Capability{
				ID:      "docker.container.rm",
				Summary: "Remove a container",
				Description: "Deletes the container and its writable layer — everything written " +
					"inside it that was not on a volume is gone, and it does not come back. Named " +
					"volumes survive; anonymous ones do not unless the daemon is asked to keep " +
					"them, and this does not ask. A stopped container is required: this deliberately " +
					"offers no --force, because \"remove\" and \"kill first, then remove\" are two " +
					"decisions and only one of them was made here.",
				// The one genuine delete in this plugin, and the place docker
				// accepts a risk kube declined for the equivalent case (no pod
				// delete). Destructive, so it is behind --allow-destructive as
				// well as a grant, and rta will preview it.
				Safety:     plugin.Destructive,
				NeedsGrant: true,
				Scope:      "container",
				Inputs:     []plugin.Field{nameInput("the container to remove — it must be stopped")},
				Run:        runRemove,
			}),
		},
	}
}
