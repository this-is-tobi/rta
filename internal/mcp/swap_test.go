package mcp

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/internal/pathguard/pathswap"
)

// The races these tests run are the attack a root cannot judge its way out
// of: a caller who may write inside it swaps the file a call names, or a
// directory above it, for a link out, between the guard judging the path and
// the handler opening it. Each runs the real capability through the real
// server, so what is proved is what an agent gets: every call answers from
// inside the root or is refused, and none, over hundreds of calls landing on
// both sides of the swap, answers from outside.

// swapServer is a server rooted at root with every built-in, its state kept
// out of the way.
func swapServer(t *testing.T, root string) *sdk.ClientSession {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ")
	}
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	// Refused calls are the swap working, and hundreds of them in a row are
	// what the refusal backoff exists to slow: it is not what is tested here.
	return connectWith(t, reg, Options{Paths: guard, refusals: newBackoff(1<<30, time.Minute, 0, 0)})
}

// swapCall is one tools/call as pathswap counts it: the text of the answer,
// refusals included, since a refusal is a channel as much as a result is.
func swapCall(s *sdk.ClientSession, tool string, args map[string]any) pathswap.Outcome {
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return pathswap.Outcome{Err: err}
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if res.IsError {
		return pathswap.Outcome{Out: text, Err: errors.New("refused")}
	}
	return pathswap.Outcome{Out: text}
}

// fileCase is a capability that reads the one file its caller names: what
// to call it with, the file's name, what it holds inside the root and what
// the file of that name outside holds, and the text that would say an
// answer came from outside.
type fileCase struct {
	tool, input, name string
	args              map[string]any
	inside, outside   []byte
	leaks             func(answer string) bool
}

// says is a leak that shows as text the answer from outside has in it.
func says(text string) func(string) bool {
	return func(answer string) bool { return strings.Contains(answer, text) }
}

// declared is a leak that shows as more dependencies declared than any
// manifest inside the root holds: an inventory names no package, only how
// many, so the manifest outside declares many more.
func declared(answer string) bool {
	m := regexp.MustCompile(`(\d+) declared`).FindStringSubmatch(answer)
	n, _ := strconv.Atoi(append(m, "0", "0")[1])
	return n >= outsideDeps
}

// outsideDeps is how many a manifest outside the root declares.
const outsideDeps = 50

func fileCases(t *testing.T) []fileCase {
	secretSum := sha256.Sum256([]byte("the operator's private key"))
	return []fileCase{
		{tool: "fs_hash", input: "path", name: "id_ed25519",
			inside: []byte("a note"), outside: []byte("the operator's private key"),
			leaks: says(hex.EncodeToString(secretSum[:]))},
		{tool: "cert_inspect", input: "target", name: "tls.pem",
			inside: selfSigned(t, "inside.example"), outside: selfSigned(t, "outside-secret.example"),
			leaks: says("outside-secret")},
		{tool: "net_hosts_list", input: "file", name: "hosts",
			inside: []byte("127.0.0.1 inside.test\n"), outside: []byte("10.66.66.66 outside-secret.corp\n"),
			leaks: says("outside-secret")},
		{tool: "net_resolver_list", input: "file", name: "resolv.conf",
			inside: []byte("nameserver 10.0.0.53\n"), outside: []byte("nameserver 10.66.66.66\nsearch outside-secret.corp\n"),
			leaks: says("outside-secret")},
		{tool: "audit_deps", input: "path", name: "go.mod", args: map[string]any{"offline": true},
			inside: goMod("inside", 3), outside: goMod("outside", outsideDeps), leaks: declared},
	}
}

func (c fileCase) run(t *testing.T, s *sdk.ClientSession, path string, swap func()) {
	t.Helper()
	args := map[string]any{c.input: path}
	for k, v := range c.args {
		args[k] = v
	}
	// Once with nothing swapped, so a case that could never answer is not
	// counted as one that never leaked.
	before := swapCall(s, c.tool, args)
	if before.Err != nil {
		t.Fatalf("%s refused the file before any swap: %s", c.tool, before.Out)
	}
	swap()
	pathswap.Run(t, before.Out, func() pathswap.Outcome { return swapCall(s, c.tool, args) },
		func(o pathswap.Outcome) bool { return c.leaks(o.Out) })
}

func TestNoCapabilityReadsAFileSwappedForALinkOut(t *testing.T) {
	for _, c := range fileCases(t) {
		t.Run(c.tool, func(t *testing.T) {
			root := t.TempDir()
			s := swapServer(t, root)
			file := filepath.Join(root, c.name)
			write(t, file, c.inside)
			outside := filepath.Join(t.TempDir(), c.name)
			write(t, outside, c.outside)
			c.run(t, s, file, func() { pathswap.File(t, file, outside) })
		})
	}
}

func TestNoCapabilityReadsThroughADirectorySwappedForALinkOut(t *testing.T) {
	for _, c := range fileCases(t) {
		t.Run(c.tool, func(t *testing.T) {
			root := t.TempDir()
			s := swapServer(t, root)
			dir := filepath.Join(root, "etc")
			write(t, filepath.Join(dir, c.name), c.inside)
			outside := t.TempDir()
			write(t, filepath.Join(outside, c.name), c.outside)
			c.run(t, s, filepath.Join(dir, c.name), func() { pathswap.Dir(t, dir, outside) })
		})
	}
}

// goMod is a go.mod requiring n modules.
func goMod(module string, n int) []byte {
	var b strings.Builder
	b.WriteString("module example.com/" + module + "\n\ngo 1.22\n\nrequire (\n")
	for i := range n {
		fmt.Fprintf(&b, "\texample.com/%s-dep%d v1.0.0\n", module, i)
	}
	b.WriteString(")\n")
	return []byte(b.String())
}

// A link out of the root is refused, and the refusal is the same whether
// what it points at exists: a caller who may write inside a root and link to
// a name outside must not learn from the answer whether anything is there —
// through a link whose target is missing, through one behind a directory the
// server may not search, or through one to a file that is plainly there.
func TestALinkOutAnswersTheSameWhetherItsTargetExists(t *testing.T) {
	for _, c := range fileCases(t) {
		t.Run(c.tool, func(t *testing.T) {
			root := t.TempDir()
			s := swapServer(t, root)
			outside := filepath.Join(t.TempDir(), c.name)
			link := filepath.Join(root, c.name)
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			args := map[string]any{c.input: link}
			for k, v := range c.args {
				args[k] = v
			}
			write(t, outside, c.outside)
			there := swapCall(s, c.tool, args)
			if err := os.Remove(outside); err != nil {
				t.Fatal(err)
			}
			gone := swapCall(s, c.tool, args)
			if there.Err == nil || gone.Err == nil {
				t.Fatalf("a link out was answered:\n  present: %s\n  missing: %s", there.Out, gone.Out)
			}
			if there.Out != gone.Out {
				t.Errorf("the answer tells whether the target exists:\n  present: %s\n  missing: %s", there.Out, gone.Out)
			}
		})
	}
}

// walkCases are the capabilities that walk the directory their caller names:
// a file in a directory beneath it, what the file holds inside the root and
// what the one outside does, and the text that would say an answer came
// from outside.
func walkCases() []fileCase {
	// Every file inside is a few bytes and the one outside is kilobytes, so a
	// size in KiB anywhere in a listing is one that counted it.
	small, large := []byte("a note"), bytes.Repeat([]byte("x"), 4096)
	return []fileCase{
		{tool: "fs_tree", input: "path", name: "notes.txt", args: map[string]any{"depth": 3},
			inside: small, outside: large, leaks: says("KiB")},
		{tool: "fs_usage", input: "path", name: "notes.txt", args: map[string]any{"detail": true},
			inside: small, outside: large, leaks: says("KiB")},
		{tool: "audit_deps", input: "path", name: "go.mod", args: map[string]any{"offline": true, "recursive": true},
			inside: goMod("inside", 3), outside: goMod("outside", outsideDeps), leaks: declared},
	}
}

// A walk goes down through directories it finds, not ones it was given, and
// each is a name a caller can swap between the walk looking at it and going
// into it; and the directory it was given can be swapped as well.
//
// The second is the wide window, and a walk by path names leaks through it
// in every run of this length. The first is a few microseconds between two
// system calls, which a walk by names leaked through about once in ten
// thousand calls that met the swap, measured — past what a test run affords,
// so pathin's own tests pin it without a race: a Dir refuses a link where it
// looked, and anything that is not what it looked at.
func TestNoWalkReadsThroughADirectorySwappedForALinkOut(t *testing.T) {
	for _, c := range walkCases() {
		for _, swapped := range []string{"the directory given", "a directory beneath it"} {
			t.Run(c.tool+"/"+swapped, func(t *testing.T) {
				root := t.TempDir()
				s := swapServer(t, root)
				project := filepath.Join(root, "project")
				// A file at the top as well, so an answer is never empty.
				write(t, filepath.Join(project, c.name), c.inside)
				write(t, filepath.Join(project, "sub", c.name), c.inside)
				outside := t.TempDir()
				write(t, filepath.Join(outside, c.name), c.outside)
				write(t, filepath.Join(outside, "sub", c.name), c.outside)
				dir := filepath.Join(project, "sub")
				if swapped == "the directory given" {
					dir = project
				}
				c.run(t, s, project, func() { pathswap.Dir(t, dir, outside) })
			})
		}
	}
}

func write(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// selfSigned is a PEM certificate for cn, which is all cert_inspect needs to
// have something to say.
func selfSigned(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
