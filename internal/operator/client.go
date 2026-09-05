package operator

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The client half: how `--server <name>` becomes a signed call. A server is
// named in remotes.yaml — a file the operator writes by hand, like the
// server's own roster — and every call is two round trips: fetch a
// challenge, send the envelope. Deliberately plain request/response over
// stdlib HTTP: no streams, no second protocol, nothing an enterprise proxy
// in the CONNECT path can break, and the default transport already honours
// HTTPS_PROXY et al.

// RemotesPath is where the server list lives: beside config.yaml, because it
// is configuration a person writes, not state rta manages.
func RemotesPath() string {
	return filepath.Join(filepath.Dir(config.Path()), "remotes.yaml")
}

// remotesFile is the on-disk shape:
//
//	servers:
//	  prod:
//	    url: https://rta.example.com
type remotesFile struct {
	Servers map[string]struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
}

// ServerURL resolves a server name to its canonical base URL. Every refusal
// names what would fix it, because this is the first thing a new setup gets
// wrong.
//
// The file gets the roster's permission discipline, because it is the same
// kind of trust anchor pointed the other way: the roster decides who may
// speak to a server, this file decides where an operator's signatures are
// aimed. Someone who can rewrite it repoints "prod" at a server they run —
// the signature binding then refuses the relay, but the misdirection alone
// is worth refusing at the source.
func ServerURL(name string) (string, *view.Error) {
	f, err := os.Open(RemotesPath())
	if err != nil {
		return "", view.Errorf("core.operator.remotes",
			"no server list at %s", RemotesPath()).
			WithHint("create it with your servers:\n  servers:\n    prod:\n      url: https://rta.example.com")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", view.Errorf("core.operator.remotes", "reading %s: %v", RemotesPath(), err)
	}
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode&0o037 != 0 {
			return "", view.Errorf("core.operator.remotes",
				"%s has weak permissions (mode %s) — someone besides its owner can write or execute it, "+
					"and it decides where your signed operator calls go", RemotesPath(), mode).
				WithHint("chmod 600 " + RemotesPath())
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", view.Errorf("core.operator.remotes", "reading %s: %v", RemotesPath(), err)
	}
	var rf remotesFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return "", view.Errorf("core.operator.remotes", "parsing %s: %v", RemotesPath(), err)
	}
	s, ok := rf.Servers[name]
	if !ok || s.URL == "" {
		known := make([]string, 0, len(rf.Servers))
		for n, srv := range rf.Servers {
			if srv.URL != "" {
				known = append(known, n)
			}
		}
		sort.Strings(known)
		hint := "add it to " + RemotesPath()
		if len(known) > 0 {
			hint = "configured servers: " + strings.Join(known, ", ")
		}
		return "", view.Errorf("core.operator.server", "%s names no server in %s", name, RemotesPath()).
			WithHint(hint)
	}
	return CanonicalServerURL(name, s.URL)
}

// CanonicalServerURL validates a server URL and returns the one spelling of
// it both ends sign and verify: lowercased host, no trailing slash. The
// signature binds the operator's call to this exact string (see message()),
// so the client canonicalising remotes.yaml's value and the server
// canonicalising --operators-url must land on the same bytes — this
// function is that agreement, shared rather than approximated twice.
//
// Plaintext is refused for anywhere but loopback — the same rule, with the
// same loopback exception for local testing, that OIDC discovery applies to
// its issuer. The signature needs no private channel, but the *response*
// does: without TLS an on-path attacker cannot forge a grant, yet can hand
// the operator a grant listing that lies, and decisions get made on that
// screen.
func CanonicalServerURL(name, raw string) (string, *view.Error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", view.Errorf("core.operator.server", "server %s has an unparseable url: %v", name, err)
	}
	secure := u.Scheme == "https" || (u.Scheme == "http" && isLoopback(u.Hostname()))
	if !secure {
		return "", view.Errorf("core.operator.insecure",
			"server %s uses %q — what it answers could be rewritten in transit", name, raw).
			WithHint("put TLS in front (a reverse proxy or tunnel); plain http is only accepted for loopback")
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.Fragment = ""
	u.RawQuery = ""
	return u.String(), nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Client calls one server as one unlocked operator.
type Client struct {
	// URL is the server's base, scheme included, no trailing slash.
	URL    string
	Signer Signer
	// HTTP overrides the default client (30s overall timeout); tests use it.
	HTTP *http.Client
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		// There are exactly two fixed endpoints and no legitimate redirect.
		// Following one would re-POST a signed envelope wherever a hostile
		// server points — the do-it-yourself version of the relay message()'s
		// binding refuses — or move the response the TLS rule protects onto
		// plaintext. Refused, not silently dropped, so a misconfigured proxy
		// reads as its own error instead of a signature mystery.
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			return errors.New("the operator channel does not follow redirects (got one to " +
				req.URL.Redacted() + ")")
		},
	}
}

// Call signs one verb and decodes the answer into out. payload may be nil.
func (c Client) Call(verb string, payload, out any) *view.Error {
	var raw []byte
	if payload != nil {
		var err error
		if raw, err = json.Marshal(payload); err != nil {
			return view.Errorf("core.operator.encode", "encoding the %s payload: %v", verb, err)
		}
	}
	res, err := c.http().Post(c.URL+"/operator/v1/challenge", "application/json", nil)
	if err != nil {
		return unreachable(c.URL, err)
	}
	var challenge struct {
		Nonce string `json:"nonce"`
	}
	decodeErr := json.NewDecoder(bounded(res.Body)).Decode(&challenge)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || decodeErr != nil || challenge.Nonce == "" {
		return view.Errorf("core.operator.protocol",
			"%s did not answer a challenge (HTTP %d) — is it an rta mcp server started with --operators?",
			c.URL, res.StatusCode)
	}
	body, err := json.Marshal(c.Signer.Sign(c.URL, challenge.Nonce, verb, raw))
	if err != nil {
		return view.Errorf("core.operator.encode", "encoding the envelope: %v", err)
	}
	res, err = c.http().Post(c.URL+"/operator/v1/call", "application/json", bytes.NewReader(body))
	if err != nil {
		return unreachable(c.URL, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		// The server's own refusal travels as a view.Error and is shown as
		// one; anything else gets the honest generic.
		var remote struct {
			Error *view.Error `json:"error"`
		}
		if err := json.NewDecoder(bounded(res.Body)).Decode(&remote); err == nil && remote.Error != nil {
			if remote.Error.Code == "core.operator.refused" {
				// The generic refusal is deliberate server-side reticence; the
				// hint belongs client-side, where the likely causes live.
				return remote.Error.WithHint("this key may not be enrolled on that server — " +
					"`rta operator status` prints the line its roster needs; the full reason is in the server's log")
			}
			return remote.Error
		}
		return view.Errorf("core.operator.protocol", "%s refused %s with HTTP %d", c.URL, verb, res.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(bounded(res.Body)).Decode(out); err != nil {
		return view.Errorf("core.operator.protocol", "decoding %s's %s answer: %v", c.URL, verb, err)
	}
	return nil
}

// bounded caps what a server's answer may cost this process: a roster read
// is a table, not a stream, and a hostile server must not be a memory bill.
func bounded(r io.Reader) io.Reader { return io.LimitReader(r, 8<<20) }

func unreachable(base string, err error) *view.Error {
	return view.Errorf("core.operator.unreachable", "cannot reach %s: %v", base, err).
		WithHint("check the url in " + RemotesPath() + ", and whatever tunnel or VPN the server sits behind")
}

// CheckLabel validates a roster label client-side, before it is printed for
// pasting — the server-side roster loader applies the same grammar, and
// failing here beats failing there.
func CheckLabel(label string) *view.Error {
	if label == "" {
		return view.Errorf("core.operator.label", "a roster label cannot be empty")
	}
	return grant.CheckAgent(label)
}
