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
		// Every name the shared list (internal/credname) calls a credential, as the
		// http dry run and the completion history read it: the record kept these in
		// clear while the dry run masked them and the history forgot them.
		{"GitLab's header", "PRIVATE-TOKEN: glpat-x", "PRIVATE-TOKEN: ***"},
		{"Vault's header", "X-Vault-Token: hvs.x", "X-Vault-Token: ***"},
		{"Google's header", "X-Goog-Api-Key: AIza0", "X-Goog-Api-Key: ***"},
		{"a bare key header", "Api-Key: k", "Api-Key: ***"},
		{"GitLab's parameter", "https://gitlab.example/api/v4/projects?private_token=glpat-x", "https://gitlab.example/api/v4/projects?private_token=***"},
		{"an api token", "https://h/x?api_token=t&page=2", "https://h/x?api_token=***&page=2"},
		{"Vault's parameter", "https://h/x?x-vault-token=hvs.x", "https://h/x?x-vault-token=***"},
		{"a session", "https://h/x?session=s1", "https://h/x?session=***"},
		{"a password, abbreviated", "https://h/x?pwd=p", "https://h/x?pwd=***"},
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
		"user@host", "Accept: application/json", "the authorization is pending", "https://h/tokens?page=1",
		"a key=value pair with no query",
	} {
		if got := Credentials(s); got != s {
			t.Errorf("%q was changed to %q", s, got)
		}
	}
}

// A parameter whose name holds a credential's word is masked even when it is
// none: the record and the completion history read a name by one list, which
// matches words inside names (credname), and the history already forgets such
// a URL. A name that only looks like a credential costs its value in the
// record; a credential the record keeps costs the credential.
func TestANameThatOnlyLooksLikeACredentialIsMaskedToo(t *testing.T) {
	if got, want := Credentials("https://h/tokens?tokenize=1"), "https://h/tokens?tokenize=***"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
