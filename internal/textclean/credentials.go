package textclean

import "regexp"

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
	s = secretParam.ReplaceAllString(s, "${1}"+Masked)
	return secretHeader.ReplaceAllString(s, "${1}"+Masked)
}

// Masked stands where a credential was.
const Masked = "***"

var (
	// scheme://, then everything up to the last @ before the path, query or
	// fragment: a password may hold an @, and the host is what follows the
	// last one (url.Parse reads it so).
	urlUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://)[^/?#\s]*@`)

	// A parameter whose name says it is one, in a query or a form body. The
	// value ends where a URL does in prose — at a quote, a backtick, an angle
	// or round bracket, a semicolon — so the quote that closes a command or a
	// message around the URL is not taken with it.
	secretParam = regexp.MustCompile(`(?i)([?&;](?:access[_-]?token|id[_-]?token|refresh[_-]?token|token|api[_-]?key|apikey|` +
		`access[_-]?key|key|secret|client[_-]?secret|password|passwd|pwd|signature|sig|auth|` +
		`x-amz-signature|x-amz-credential|x-amz-security-token)=)[^&#\s"'\x60<>);]*`)

	// A header whose name says it carries one, at the start of a value or of a
	// line: the value of every header in a list of them is one string.
	secretHeader = regexp.MustCompile(`(?im)(^[ \t]*(?:authorization|proxy-authorization|cookie|set-cookie|x-api-key|` +
		`x-auth-token|x-amz-security-token)[ \t]*:[ \t]*)[^\r\n]*`)
)
