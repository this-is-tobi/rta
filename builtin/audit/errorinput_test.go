package audit

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	stdnet "net"
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A refusal of one input names it (view.Error.Input), so a form opens again
// on that box and an agent knows which argument to send again: a host or a
// domain no request could be about, a selector that is not one, a path that
// names nothing a dependency audit reads, a package the project does not
// declare, a namespace that is not one or is not there, an images entry that
// does not read. A host that did not answer, a resolver that did not, and a
// cluster refusing the credential name none, since no one box holds the fix.
func TestRefusalsNameTheInputTheyAreAbout(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	project := filepath.Join(dir, "project")
	for _, d := range []string{empty, project} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(project, "package-lock.json"), []byte(npmProject), 0o600); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notes, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")
	closed, err := stdnet.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refusing := closed.Addr().String()
	_ = closed.Close()

	// No timeout where the call is refused before it reaches anything: one
	// that got through by mistake ends at once rather than asking the network.
	web := func(values map[string]any) func() error {
		return func() error { _, err := runWeb(t.Context(), req(values)); return err }
	}
	mail := func(values map[string]any) func() error {
		return func() error { _, err := runMail(t.Context(), req(values)); return err }
	}
	deps := func(path string) func() error {
		return func() error {
			_, err := runDeps(t.Context(), req(map[string]any{"path": path, "offline": true}))
			return err
		}
	}
	why := func(pkg, path string) func() error {
		return func() error {
			_, err := runWhy(t.Context(), req(map[string]any{"package": pkg, "path": path}))
			return err
		}
	}
	kube := func(run func(context.Context, plugin.Request) (view.View, error), ns, kubectl string) func() error {
		return func() error {
			kubectlSaying(t, kubectl)
			_, err := run(t.Context(), newScopedRequest(ns))
			return err
		}
	}
	kubeEOL := func(images ...string) func() error {
		return func() error {
			fakeKubectl(t, map[string]string{"version": clusterVersionBody, "nodes": clusterNodesBody,
				"pods": podsRunning(t, "me/app:1")})
			srv, _ := newEOLServer(t)
			_, err := runKubeEOLAt(t.Context(), eolRequest(map[string]any{"images": images}), srv.URL)
			return err
		}
	}
	domainOf := func(res *stdnet.Resolver, f mailFacts) func() error {
		return func() error {
			if verr := requireDomain(t.Context(), res, f); verr != nil {
				return verr
			}
			return nil
		}
	}
	boom := errors.New("server misbehaving")
	const lists = `echo '{"items":[]}'` + "\n"
	const nope = `case "$*" in 'get namespace nope '*) ` +
		`echo 'Error from server (NotFound): namespaces "nope" not found' >&2; exit 1;; esac` + "\n" + lists
	const forbidden = `echo 'Error from server (Forbidden): pods is forbidden: User "u" cannot list resource "pods"' >&2` +
		"\nexit 1\n"

	for _, tc := range []struct {
		name, capID, code, input string
		call                     func() error
	}{
		{"a host with white space around it", "audit.web", "audit.web.badhost", "host",
			web(map[string]any{"host": " example.invalid"})},
		{"a host holding a character that draws as nothing", "audit.web", "audit.web.badhost", "host",
			web(map[string]any{"host": "example\u200b.invalid"})},
		{"a host no URL parser takes", "audit.web", "audit.web.badhost", "host",
			web(map[string]any{"host": "https://[::1"})},
		{"a host behind a credential", "audit.web", "audit.web.badhost", "host",
			web(map[string]any{"host": "https://user@example.invalid"})},
		{"a value that names no host", "audit.web", "audit.web.badhost", "host",
			web(map[string]any{"host": "https:///x"})},
		{"a host the request would map to another", "audit.web", "audit.web.badhost", "host",
			web(map[string]any{"host": "https://%E2%80%8Bexample.invalid"})},
		{"a host that does not answer", "audit.web", "audit.web.unreachable", "",
			web(map[string]any{"host": "https://" + refusing, "timeout": "5s"})},

		{"a domain with white space around it", "audit.mail", "audit.mail.baddomain", "domain",
			mail(map[string]any{"domain": " example.invalid"})},
		{"a domain behind a credential", "audit.mail", "audit.mail.baddomain", "domain",
			mail(map[string]any{"domain": "https://someone@example.invalid"})},
		{"an address for a domain", "audit.mail", "audit.mail.baddomain", "domain",
			mail(map[string]any{"domain": "192.0.2.1"})},
		{"a selector that is not one", "audit.mail", "audit.mail.badselector", "selector",
			mail(map[string]any{"domain": "example.invalid", "selector": "not a selector"})},
		{"a domain that does not exist", "audit.mail", "audit.mail.nxdomain", "domain",
			domainOf(nxResolver(), mailFacts{domain: "nx.example.invalid"})},
		{"a domain no lookup about was answered", "audit.mail", "audit.mail.resolver", "",
			domainOf(nil, mailFacts{domain: "d.test", apexErr: boom, dmarcErr: boom, stsErr: boom, rptErr: boom,
				mxErr: boom})},

		{"a path that is not there", "audit.deps", "audit.deps.nopath", "path", deps(missing)},
		{"a directory with no manifest", "audit.deps", "audit.deps.nomanifest", "path", deps(empty)},
		{"a file that is no manifest", "audit.deps", "audit.deps.format", "path", deps(notes)},

		{"a package of white space", "audit.why", "audit.why.nopackage", "package", why("  ", project)},
		{"a package the project does not declare", "audit.why", "audit.why.absent", "package",
			why("nosuchpackage", project)},
		{"a path that is not there, asked why", "audit.why", "audit.deps.nopath", "path", why("lodash", missing)},
		{"a directory with no manifest, asked why", "audit.why", "audit.why.nomanifest", "path",
			why("lodash", empty)},
		{"a file that is no manifest, asked why", "audit.why", "audit.deps.format", "path", why("lodash", notes)},

		{"a namespace that is not a name", "audit.kube.quotas", "audit.kube.namespace.invalid", "namespace",
			kube(runKubeQuotas, " prod", lists)},
		{"a namespace that does not exist", "audit.kube.rbac", "audit.kube.namespace.notfound", "namespace",
			kube(runKubeRBAC, "nope", nope)},
		{"a cluster refusing the credential", "audit.kube.podsecurity", "audit.kube.denied", "",
			kube(runKubePodSecurity, "", forbidden)},
		{"an images entry that does not read", "audit.kube.eol", "audit.kube.eol.images.invalid", "images",
			kubeEOL("not-an-entry")},
		{"an images suffix given twice", "audit.kube.eol", "audit.kube.eol.images.invalid", "images",
			kubeEOL("me/app=postgresql", "me/app=redis")},
		{"an images entry naming a product nobody lists", "audit.kube.eol", "audit.kube.eol.images.unknown", "images",
			kubeEOL("me/app=nosuchproduct")},
	} {
		verr := view.AsError(tc.call(), "audit.test")
		if verr == nil || verr.Code != tc.code {
			t.Errorf("%s: got %v, want %s", tc.name, verr, tc.code)
			continue
		}
		if verr.Input != tc.input {
			t.Errorf("%s: %s names the input %q, want %q", tc.name, tc.code, verr.Input, tc.input)
		}
		if _, declared := auditCapability(t, tc.capID).ErrorInput(verr); tc.input != "" && !declared {
			t.Errorf("%s: %s names %q, which %s does not declare", tc.name, tc.code, tc.input, tc.capID)
		}
	}
}

func auditCapability(t *testing.T, id string) plugin.Capability {
	t.Helper()
	for _, c := range Plugin(nil, nil).Capabilities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no capability %q", id)
	return plugin.Capability{}
}

// kubectlSaying installs a kubectl that runs script, the body of a shell
// script, in place of the real one for the rest of the test.
func kubectlSaying(t *testing.T, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubectl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := kubectlBin
	kubectlBin = path
	t.Cleanup(func() { kubectlBin = orig })
}

// nxResolver answers every question itself, over a pipe rather than the
// network, that the name does not exist: the question sent back as its own
// answer, flagged a response (QR, RD and RA) whose code is NXDOMAIN (3).
func nxResolver() *stdnet.Resolver {
	return &stdnet.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (stdnet.Conn, error) {
		client, server := stdnet.Pipe()
		go func() {
			defer server.Close()
			var size [2]byte
			if _, err := io.ReadFull(server, size[:]); err != nil {
				return
			}
			msg := make([]byte, binary.BigEndian.Uint16(size[:]))
			if _, err := io.ReadFull(server, msg); err != nil || len(msg) < 4 {
				return
			}
			msg[2], msg[3] = 0x81, 0x83
			_, _ = server.Write(append(size[:], msg...))
		}()
		return client, nil
	}}
}
