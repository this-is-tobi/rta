package textclean

import (
	"regexp"
	"strings"

	"github.com/this-is-tobi/rta/internal/credname"
)

// Credentials masks what in a piece of text is a credential by its shape: the
// userinfo of a URL, a query parameter that carries a token or a key, and an
// HTTP header that carries one.
//
// For the places text is kept, which is a different thing from where it is
// shown: the agent log is sealed, permanent and meant to be read, and what an
// agent sent as a URL or a header was written into it as it was sent.
// Declaring an input a Secret masks a whole value, and that is the answer for a
// field that is a credential; it does nothing for a field that is a URL or a
// header, which is a field that may carry one — `https://user:pass@host/`, a
// `?token=` on a link, `Authorization: Bearer …` among a list of headers — and
// which only the shape of the value says. The host, the path and every part
// that is not the secret stay, because they are what the record is for.
//
// A heuristic, and meant as one: it recognises the common shapes and cannot
// recognise a credential that looks like anything else. What it never does is
// leave a recognised one in the clear, or touch text that has none.
func Credentials(s string) string {
	if s == "" {
		return s
	}
	s = urlUserinfo.ReplaceAllString(s, "${1}"+Masked+"@")
	s = maskNamed(s, queryParam, credname.Param)
	return maskNamed(s, headerLine, credname.Header)
}

// maskNamed masks the value of every match of re whose name, re's second
// group, is one a credential is kept under; the first group is everything
// before the value, and stays.
func maskNamed(s string, re *regexp.Regexp, credential func(string) bool) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		sub := re.FindStringSubmatch(m)
		if !credential(sub[2]) {
			return m
		}
		return sub[1] + Masked
	})
}

// Masked stands where a credential was.
const Masked = "***"

// Userinfo masks the userinfo of a value that is one URL — a proxy variable,
// an agent gateway's base URL — whole, username included: the host is the
// useful part, the userinfo is the secret. A value with no userinfo comes
// back as it is.
//
// For one setting's value, where Credentials is for text: the whole value is
// what a client reads, so the rules are the client's, and they differ from
// text's in two places. A value may have no scheme, and an `@` anywhere in it
// may be the one that ends a userinfo.
//
// **Parsed by hand rather than by url.Parse, because url.Parse does not read
// every working value as a URL.** Given `bob:s3cret@proxy.corp:3128` — with
// no scheme, which is what a great many shells actually export as a proxy —
// url.Parse takes `bob` for the scheme and the rest for an opaque body, so
// u.User is nil, and net.overview printed the password in the clear directly
// underneath its own published promise that proxy credentials are masked. It
// is not a form to dismiss as malformed: golang.org/x/net/http/httpproxy
// re-parses a schemeless value with `http://` prepended, so the credential
// works, and rta's own http plugin reaches through it via DefaultTransport.
// And url.Parse refuses a userinfo holding a space, a `^`, a `{` or a `|`,
// which the URL parsers agents are written with percent-encode and send. A
// value that authenticates is a value that has to be masked.
//
// Scanning for the userinfo covers every spelling with one rule and no
// dependency on a parser's opinion of what a URL is.
func Userinfo(raw string) string {
	// A `://` is the end of a scheme only when what stands before it is one
	// (isScheme). The first one anywhere in the value was taken for it, so
	// `bob:s3cret@proxy.corp:3128/x://y` — schemeless, and read by net/http
	// as bob with s3cret, since the path is not its business — was split
	// after the credential, and printed whole; and `bob:x://s3cret@…`, whose
	// password holds the `://`, printed the username and the password's
	// first letter as though they were a scheme.
	prefix, rest := "", raw
	if i := strings.Index(raw, "://"); i >= 0 && isScheme(raw[:i]) {
		prefix, rest = raw[:i+3], raw[i+3:]
	}
	// **The last `@`, not the first**, because that is where url.Parse splits
	// — and url.Parse is what net/http reads a proxy variable with. A password
	// holding a raw `@`, `bob:P@ssw0rd@proxy.corp:3128`, is a working
	// configuration that authenticates as bob with P@ssw0rd; cut at the first
	// `@`, this printed `***@ssw0rd@proxy.corp:3128`, most of the password.
	//
	// **And the last in the whole value, not in its authority.** The scan
	// stopped at the authority's end, the first `/`, `?` or `#`, so that an
	// `@` in a path would not mask the host — and a raw `/`, `?` or `#` in a
	// password ends the authority just as early. `bob:2024/Secret@proxy.corp`
	// is, to url.Parse, a proxy at bob:2024 with a path and no userinfo, and
	// the scan, finding no `@` in that authority, printed the password whole.
	// `bob:s3/cr3t@…`, which url.Parse refuses for its port, was printed the
	// same way. Nothing in such a value tells the password from a path, so the
	// rare value whose path holds an `@` loses its host to the mask, and a
	// password never reaches the screen; a proxy's path, query and fragment
	// are never read, by net/http or by the SOCKS dialer, so for a proxy
	// nothing is lost at all.
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return raw
	}
	return prefix + Masked + "@" + rest[at+1:]
}

var (
	// scheme://, then everything up to the last @ before the path, query or
	// fragment: a password may hold an @, and the host is what follows the
	// last one (url.Parse reads it so).
	urlUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://)[^/?#\s]*@`)

	// A parameter in a query or a form body, whose name is held to credname's
	// list: the one the http dry run and the completion history read. This
	// kept a list of its own, of exact names, and the sealed record wrote down
	// the `?private_token=`, `?api_token=` and `?session=` the history forgot.
	// The list matches words inside a name, so a `?tokenize=` loses its value
	// too, which is the side to be wrong on in a record kept for good. The
	// value ends where a URL does in prose — at a quote, a backtick, an angle
	// or round bracket, a semicolon — so the quote that closes a command or a
	// message around the URL is not taken with it.
	queryParam = regexp.MustCompile(`([?&;]([^=&#?;\s"'\x60<>()]+)=)[^&#\s"'\x60<>);]*`)

	// A header at the start of a value or of a line, the value of every header
	// in a list of them being one string, its name held to the same list:
	// GitLab's PRIVATE-TOKEN, Vault's X-Vault-Token and Google's X-Goog-Api-Key
	// were masked by the dry run and kept in clear by the record.
	headerLine = regexp.MustCompile(`(?m)(^[ \t]*([!#$%&'*+.^_\x60|~0-9A-Za-z-]+)[ \t]*:[ \t]*)[^\r\n]*`)
)
