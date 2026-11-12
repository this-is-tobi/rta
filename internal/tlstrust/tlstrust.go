// Package tlstrust remembers the certificates the operator trusted for a
// server on first use: a server whose certificate nothing on this machine
// vouches for — a self-signed appliance, a cluster's own CA, a certificate
// that expired — is reached once somebody said it is the one, and refused
// the day it presents anything else, the way ssh treats a host key.
//
// **What it stands in for, and what it does not.** A plugin checks a server as
// it always did first (plugin.Service's TLS), against this machine's roots;
// what is remembered here is read only when that fails and no CA was named
// for the connection, since a CA the operator named is the only issuer they
// vouch for there. A trusted certificate never narrows what verified before.
//
// **Who decides.** Through a `kube:` forward the first certificate is trusted
// as it arrives: the path there is kubectl's own tunnel into the pod, which a
// network between here and the cluster cannot sit on, and the forward is the
// operator's coordinate, which no agent can name. On any other path the first
// one waits — the call is refused, and the certificate kept as pending until
// the operator, at a terminal, looks at it and says yes (`rta cert trust`).
// Silent there, a café's Wi-Fi that answers DNS and the gateway would have
// its own certificate kept for a server that normally has a valid one, and
// the credential handed over with the first call. A certificate other than
// the one trusted always waits, forward or not, and nothing an agent can
// send answers either question: the commands are a person's alone.
//
// **Sealed, like grants and locks** (internal/seal): what is trusted here
// decides which server a plugin hands its credential to, and a file a
// confined plugin could write without reading would point another plugin's
// credential at a server of its choosing.
package tlstrust

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/filelock"
	"github.com/this-is-tobi/rta/internal/seal"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

const (
	fileName = "tls-trust.json"
	keyFile  = "tls-trust.key"
	// maxFile bounds every read, which happens before the seal is checked, so
	// a forged file costs no more than this to refuse. A record is a few
	// certificates of a kilobyte or two each.
	maxFile = 1 << 20
	// maxServers bounds the records, which only the operator's own
	// connections create — a server is where a profile or config points, and
	// an agent names none — so the bound is for a store that would otherwise
	// grow without one, never for anybody's working set.
	maxServers = 256
	// maxChain and maxCert bound what one record keeps of what a plugin
	// reported, which is a plugin's word and read as one: a chain is a leaf and
	// an intermediate or two, a certificate a kilobyte or two.
	maxChain = 8
	maxCert  = 16 << 10
)

// now is overridable in tests, which read the time a record says.
var now = time.Now

// How is the way a server came to be trusted.
type How string

const (
	// ByForward is trusted as it arrived through a `kube:` forward, which
	// nobody was asked about.
	ByForward How = "forward"
	// ByOperator is trusted on the operator's word at a terminal.
	ByOperator How = "operator"
)

// Seen is one chain a server presented, leaf first, as DER.
type Seen struct {
	Chain [][]byte  `json:"chain"`
	At    time.Time `json:"at"`
	// Profile is the profile the call that met it came through, "" for none:
	// where the operator is told it was seen.
	Profile string `json:"profile,omitempty"`
	// How is set on a trusted chain only.
	How How `json:"how,omitempty"`
}

// Fingerprint is the SHA-256 of the leaf, in hex: the digest `rta cert
// inspect` prints, so the one a refusal names is the one a person compares.
func (s Seen) Fingerprint() string { return Fingerprint(s.Chain) }

// Leaf is the server's own certificate, parsed, or nil when the chain has
// none that parses.
func (s Seen) Leaf() *x509.Certificate {
	if len(s.Chain) == 0 {
		return nil
	}
	c, err := x509.ParseCertificate(s.Chain[0])
	if err != nil {
		return nil
	}
	return c
}

// Fingerprint is the SHA-256 of chain's leaf, in hex, "" for no chain.
func Fingerprint(chain [][]byte) string {
	if len(chain) == 0 {
		return ""
	}
	sum := sha256.Sum256(chain[0])
	return hex.EncodeToString(sum[:])
}

// Server is what is remembered about one server, for one plugin.
//
// Per plugin and not per server alone: a plugin's report of what a server
// presented is its own word, and a record two plugins shared would let one
// of them decide what the other accepts.
type Server struct {
	Plugin string `json:"plugin"`
	// Server is where the calls went (ServerOf): host:port, or the forward's
	// coordinate.
	Server  string `json:"server"`
	Trusted *Seen  `json:"trusted,omitempty"`
	// Pending is a chain the server presented and was refused for, waiting
	// for the operator: the first one, or one other than the one trusted.
	Pending *Seen `json:"pending,omitempty"`
}

// ServerOf is the server a call reaches, as trust remembers it, or "" when
// the inputs name none: the forward's coordinate when the call went through
// one, since there the address is 127.0.0.1 and a port that changes every
// call; otherwise the host and port of the endpoint the declared inputs name
// (plugin.Field.Endpoint), host lowercased, the port a URL's scheme implies
// filled in.
func ServerOf(inputs []plugin.Field, req plugin.Request) string {
	if c := req.Coordinate(); c != "" {
		return c
	}
	var host string
	port := 0
	for _, f := range inputs {
		switch f.Endpoint {
		case plugin.EndpointURL:
			return urlServer(req.String(f.Name))
		case plugin.EndpointAddress:
			h, p, err := net.SplitHostPort(strings.TrimSpace(req.String(f.Name)))
			if err != nil || h == "" || p == "" {
				return ""
			}
			return joined(h, p)
		case plugin.EndpointHost:
			host = strings.TrimSpace(req.String(f.Name))
		case plugin.EndpointPort:
			port = req.Int(f.Name)
		}
	}
	if host == "" || port <= 0 {
		return ""
	}
	return joined(host, strconv.Itoa(port))
}

func urlServer(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		default:
			return ""
		}
	}
	return joined(u.Hostname(), port)
}

// joined is host and port as one name for one server: DNS names a host in any
// case and with or without its final dot.
func joined(host, port string) string {
	return net.JoinHostPort(strings.TrimSuffix(strings.ToLower(host), "."), port)
}

// noter is the key a call's context carries its surface's ear under (Noting).
type noter struct{}

// Noting is ctx carrying note, for the surface that runs a call to hear what
// trust did during it: a certificate trusted as it arrived through a forward,
// or kept for the operator, during an agent's call is what the operator reads
// in that call's record. A context value, since the trusting happens below
// every surface, at the host's door to a plugin, and only one surface keeps a
// record.
func Noting(ctx context.Context, note func(string)) context.Context {
	return context.WithValue(ctx, noter{}, note)
}

// Note tells ctx's surface what trust did, when one is listening.
func Note(ctx context.Context, what string) {
	if note, ok := ctx.Value(noter{}).(func(string)); ok && note != nil {
		note(what)
	}
}

// Path is where what is trusted lives.
func Path() string { return seal.Path(fileName) }

func keyPath() string { return seal.Path(keyFile) }

// recoveryHint is what an operator does about a store that does not verify:
// nothing in it is honoured until then, so each server is trusted again.
func recoveryHint() string {
	return "at the machine's terminal, delete " + Path() + " and, if the key beside it is short, " + keyPath() +
		"; each server is then trusted again the first time a call meets it"
}

type sealed struct {
	Seal    string   `json:"seal"`
	Servers []Server `json:"servers"`
}

// canonical is what the seal covers: the parsed records re-marshalled, so a
// re-indented file still verifies and only a change inside a record is a
// forgery (internal/lockdown's canonical has the reasoning).
func canonical(servers []Server) ([]byte, error) { return json.Marshal(servers) }

func sealKey(create bool) ([]byte, *view.Error) {
	key, err := seal.Key(keyFile, create)
	switch {
	case err == nil:
		return key, nil
	case errors.Is(err, seal.ErrMissing):
		return nil, view.Errorf("core.trust.unsealed",
			"%s exists with no seal key beside it, so it was not written by rta", Path()).WithHint(recoveryHint())
	case errors.Is(err, seal.ErrShort):
		return nil, view.Errorf("core.trust.unsealed",
			"%s is too short to be a seal key, so nothing trusted can be sealed or checked against it", keyPath()).
			WithHint(recoveryHint())
	}
	code := "core.trust.write"
	if !create {
		code = "core.trust.read"
	}
	return nil, view.Errorf(code, "%v", err)
}

func load() ([]Server, *view.Error) {
	data, err := atomicfile.ReadCapped(Path(), maxFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, view.Errorf("core.trust.read", "%v", err)
	}
	key, verr := sealKey(false)
	if verr != nil {
		return nil, verr
	}
	var doc sealed
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, view.Errorf("core.trust.forged", "%s does not parse, so it was not written by rta", Path()).
			WithHint(recoveryHint())
	}
	raw, err := canonical(doc.Servers)
	if err != nil {
		return nil, view.Errorf("core.trust.read", "%v", err)
	}
	if !seal.Equal(doc.Seal, seal.MAC(key, raw)) {
		return nil, view.Errorf("core.trust.forged", "%s does not carry rta's own seal, so something else wrote it",
			Path()).WithHint(recoveryHint())
	}
	return doc.Servers, nil
}

func save(servers []Server) *view.Error {
	key, verr := sealKey(true)
	if verr != nil {
		return verr
	}
	raw, err := canonical(servers)
	if err != nil {
		return view.Errorf("core.trust.write", "%v", err)
	}
	data, err := json.MarshalIndent(sealed{Seal: seal.MAC(key, raw), Servers: servers}, "", "  ")
	if err != nil {
		return view.Errorf("core.trust.write", "%v", err)
	}
	if err := atomicfile.Write(Path(), data, 0o600); err != nil {
		return view.Errorf("core.trust.write", "%v", err)
	}
	return nil
}

// mutate rewrites the store under its lock, f saying whether anything
// changed: what a caller is told is decided under the same lock as the write,
// so the two cannot disagree. A file that does not verify is neither honoured
// nor built upon, since writing over bytes of unknown authorship would seal
// them as rta's own.
func mutate(f func([]Server) ([]Server, bool)) *view.Error {
	release, err := filelock.Acquire(Path()+".lock", 10*time.Second, 25*time.Millisecond, 5*time.Second)
	if err != nil {
		return filelock.Failed(err, "core.trust.busy", "another rta is changing what is trusted")
	}
	defer release()
	stored, verr := load()
	if verr != nil {
		return verr
	}
	next, write := f(stored)
	if !write {
		return nil
	}
	return save(next)
}

func find(servers []Server, ns, server string) int {
	return slices.IndexFunc(servers, func(s Server) bool { return s.Plugin == ns && s.Server == server })
}

func sameChain(a, b [][]byte) bool {
	return slices.EqualFunc(a, b, bytes.Equal)
}

// List is every server remembered, in the order they were first met.
func List() ([]Server, *view.Error) { return load() }

// Trusted is the chain trusted for ns's calls to server, nil for none. A store
// that does not verify answers its refusal, and its caller trusts nothing.
func Trusted(ns, server string) ([][]byte, *view.Error) {
	servers, verr := load()
	if verr != nil {
		return nil, verr
	}
	if i := find(servers, ns, server); i >= 0 && servers[i].Trusted != nil {
		return servers[i].Trusted.Chain, nil
	}
	return nil, nil
}

// Outcome is what Record did with a chain a server presented.
type Outcome int

const (
	// Waiting is a chain kept for the operator to look at: the call stays
	// refused.
	Waiting Outcome = iota
	// TrustedNow is a chain trusted as it arrived (ByForward): the call is
	// made again, with it.
	TrustedNow
	// Known is a chain already trusted: the refusal is about something else,
	// and nothing was written.
	Known
)

// Record remembers the chain a server presented to ns's call through profile
// when its certificate was refused: trusted as it arrived when auto says the
// call came through a `kube:` forward and nothing is trusted for the server
// yet, and otherwise kept as pending, for the operator. A chain already
// pending is left as it was, first seen when it was first seen, so a tile
// refused every few seconds writes the store once.
//
// A chain that is no certificate is not one a server presented, whatever the
// plugin says, and is recorded nowhere (Known, and nothing written).
func Record(ns, server, profile string, chain [][]byte, auto bool) (Outcome, Server, *view.Error) {
	if !plausible(chain) {
		return Known, Server{}, nil
	}
	if len(chain) > maxChain {
		chain = chain[:maxChain]
	}
	outcome, rec := Known, Server{}
	var full bool
	verr := mutate(func(servers []Server) ([]Server, bool) {
		i := find(servers, ns, server)
		if i < 0 {
			if len(servers) >= maxServers {
				full = true
				return servers, false
			}
			servers = append(servers, Server{Plugin: ns, Server: server})
			i = len(servers) - 1
		}
		s := &servers[i]
		seen := &Seen{Chain: chain, At: now().UTC().Truncate(time.Second), Profile: profile}
		switch {
		case s.Trusted != nil && sameChain(s.Trusted.Chain, chain):
			rec = *s
			return servers, false
		case auto && s.Trusted == nil:
			seen.How = ByForward
			s.Trusted, s.Pending = seen, nil
			outcome = TrustedNow
		case s.Pending != nil && sameChain(s.Pending.Chain, chain):
			outcome, rec = Waiting, *s
			return servers, false
		default:
			s.Pending = seen
			outcome = Waiting
		}
		rec = *s
		return servers, true
	})
	if verr != nil {
		return Known, Server{}, verr
	}
	if full {
		return Known, Server{}, view.Errorf("core.trust.full",
			"rta remembers %d servers' certificates already, and keeps no more", maxServers).
			WithHint("`rta cert trusted` lists them, and `rta cert forget` takes back one no connection reaches any more")
	}
	return outcome, rec, nil
}

// plausible reports whether chain is a chain of certificates of a sane size.
func plausible(chain [][]byte) bool {
	if len(chain) == 0 {
		return false
	}
	for _, der := range chain {
		if len(der) == 0 || len(der) > maxCert {
			return false
		}
		if _, err := x509.ParseCertificate(der); err != nil {
			return false
		}
	}
	return true
}

// Trust trusts the chain pending for ns's calls to server, on the operator's
// word, when its leaf is still the one whose fingerprint they were shown: a
// server that presented another since then is one nobody looked at, and the
// answer is refused rather than given to it.
func Trust(ns, server, fingerprint string) (Server, *view.Error) {
	var rec Server
	var refusal *view.Error
	verr := mutate(func(servers []Server) ([]Server, bool) {
		i := find(servers, ns, server)
		if i < 0 || servers[i].Pending == nil {
			refusal = view.Errorf("core.trust.none", "no certificate %s presented to %s waits to be trusted", server, ns).
				WithHint("a call that meets a certificate nothing here trusts keeps it here; `rta cert trusted` lists " +
					"what is kept")
			return servers, false
		}
		pending := *servers[i].Pending
		if pending.Fingerprint() != fingerprint {
			refusal = view.Errorf("core.trust.moved", "%s presented another certificate since it was shown", server).
				WithHint("look at it again: `rta cert trust " + server + "` shows the one it presents now")
			return servers, false
		}
		pending.How, pending.At = ByOperator, now().UTC().Truncate(time.Second)
		servers[i].Trusted, servers[i].Pending = &pending, nil
		rec = servers[i]
		return servers, true
	})
	if verr != nil {
		return Server{}, verr
	}
	if refusal != nil {
		return Server{}, refusal
	}
	return rec, nil
}

// Forget takes back everything remembered about server for ns — every plugin
// when ns is "" — and reports how many records went: the next call there meets
// its certificate as a first one.
func Forget(ns, server string) (int, *view.Error) {
	gone := 0
	verr := mutate(func(servers []Server) ([]Server, bool) {
		kept := servers[:0]
		for _, s := range servers {
			if s.Server == server && (ns == "" || s.Plugin == ns) {
				gone++
				continue
			}
			kept = append(kept, s)
		}
		return kept, gone > 0
	})
	return gone, verr
}
