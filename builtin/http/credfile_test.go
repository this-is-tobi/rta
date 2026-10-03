package http

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func authSeenBy(t *testing.T, values map[string]any) (auth string, err error) {
	t.Helper()
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		auth = r.Header.Get("Authorization")
	}))
	defer srv.Close()
	values["url"] = srv.URL
	_, err = doRequest(context.Background(), "GET", req(values))
	return auth, err
}

func credentialFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cred")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// --bearer and --basic put a credential in the shell's history and, while the
// call runs, in a process table every user can read. A file, or /dev/stdin
// for a pipe, is the way in that does neither; the line break an editor or
// `echo` leaves is no part of the token.
func TestACredentialCanComeFromAFileInsteadOfAnArgument(t *testing.T) {
	got, err := authSeenBy(t, map[string]any{"bearer-file": credentialFile(t, "s3cret-token\n")})
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer s3cret-token" {
		t.Errorf("Authorization = %q, want the token read from the file without its line break", got)
	}

	got, err = authSeenBy(t, map[string]any{"basic-file": credentialFile(t, "alice:pa:ss\r\n")})
	if err != nil {
		t.Fatal(err)
	}
	if want := "Basic YWxpY2U6cGE6c3M="; got != want {
		t.Errorf("Authorization = %q, want %q (alice and pa:ss)", got, want)
	}
}

func TestACredentialGivenTwiceOrEmptyIsRefused(t *testing.T) {
	_, err := authSeenBy(t, map[string]any{"bearer": "a", "bearer-file": credentialFile(t, "b")})
	if verr := view.AsError(err, "x"); err == nil || verr.Code != "http.auth.twice" {
		t.Errorf("a token as a value and a file: %v, want http.auth.twice", err)
	}
	_, err = authSeenBy(t, map[string]any{"bearer-file": credentialFile(t, "\n")})
	if verr := view.AsError(err, "x"); err == nil || verr.Code != "http.auth.file" {
		t.Errorf("an empty file: %v, want http.auth.file", err)
	}
	_, err = authSeenBy(t, map[string]any{"bearer-file": filepath.Join(t.TempDir(), "absent")})
	if verr := view.AsError(err, "x"); err == nil || verr.Code != "http.auth.file" {
		t.Errorf("a file that is not there: %v, want http.auth.file", err)
	}
}

// A path is a place on this machine, and an agent that could name one would
// read any file the server can into a request to a host of its own choosing:
// the inputs are Local, so no agent's schema offers them.
func TestTheCredentialFileInputsAreNeverOfferedToAnAgent(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.ID == "http.status" {
			continue
		}
		for _, name := range []string{"bearer-file", "basic-file"} {
			var found bool
			for _, f := range c.Inputs {
				if f.Name == name {
					found = true
					if !f.Local || f.Type != plugin.Path {
						t.Errorf("%s.%s is not a Local path", c.ID, name)
					}
				}
			}
			if !found {
				t.Errorf("%s has no %s", c.ID, name)
			}
		}
	}
}
