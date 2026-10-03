package textclean

import "testing"

func TestCredentialsMasksWhatIsACredentialByItsShape(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"userinfo", "https://alice:s3cret@api.example.com/v1", "https://***@api.example.com/v1"},
		{"a name alone", "https://alice@api.example.com/", "https://***@api.example.com/"},
		{"an @ in the password", "https://bob:P@ssw0rd@host.example/x", "https://***@host.example/x"},
		{"a scheme that is not http", "postgres://u:p@db.internal:5432/app", "postgres://***@db.internal:5432/app"},
		{"in the middle of a sentence", "GET https://u:p@h/x failed: refused", "GET https://***@h/x failed: refused"},
		{"a token in the query", "https://h/x?token=abc123&page=2", "https://h/x?token=***&page=2"},
		{"a key and a signature", "https://h/x?a=1&api_key=k1&X-Amz-Signature=deadbeef", "https://h/x?a=1&api_key=***&X-Amz-Signature=***"},
		{"in either case", "https://h/x?Access_Token=zz#frag", "https://h/x?Access_Token=***#frag"},
		{"quoted in a command", "run `rta grant allow web.get 'https://h/x?token=abc' --ttl 15m`", "run `rta grant allow web.get 'https://h/x?token=***' --ttl 15m`"},
		{"quoted in a message", `Get "https://h/x?key=abc": connection refused`, `Get "https://h/x?key=***": connection refused`},
		{"in brackets", "(see https://h/x?token=abc) and <https://h/y?sig=q>", "(see https://h/x?token=***) and <https://h/y?sig=***>"},
		{"a bearer header", "Authorization: Bearer abc.def.ghi", "Authorization: ***"},
		{"a cookie header", "cookie: sid=1; theme=dark", "cookie: ***"},
		{"headers one to a line", "Accept: */*\nX-Api-Key: k\nUser-Agent: rta", "Accept: */*\nX-Api-Key: ***\nUser-Agent: rta"},
	} {
		if got := Credentials(tc.in); got != tc.want {
			t.Errorf("%s: %q becomes %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// What is not a credential is left exactly as it was: the record exists to say
// what was asked for.
func TestCredentialsLeavesWhatIsNotOneAlone(t *testing.T) {
	for _, s := range []string{
		"", "plain text", "https://api.example.com/v1/items?page=2&limit=5", "mailto:someone@example.com",
		"user@host", "Accept: application/json", "the authorization is pending", "https://h/tokens?tokenize=1",
		"a key=value pair with no query",
	} {
		if got := Credentials(s); got != s {
			t.Errorf("%q was changed to %q", s, got)
		}
	}
}
