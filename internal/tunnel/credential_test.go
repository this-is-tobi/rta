package tunnel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// One kubectl message reaches three classifiers, and each read it as
// something else: a missing service on the forward, a missing secret on the
// read, an expired login on a listing. All three now name the tool, because
// installing it is the fix. Both spellings kubectl uses, captured from a
// real one: a bare name looked up on $PATH, and an absolute path.
func TestAMissingCredentialPluginIsNamedOnEverySurface(t *testing.T) {
	for exe, stderr := range map[string]string{
		"tsh": `Unable to connect to the server: getting credentials: exec: executable tsh not found`,
		"acme-auth": `E0921 04:13:36.061482 94227 memcache.go:265] "Unhandled Error" err="couldn't get current server API group list: Get \"https://127.0.0.1:1/api?timeout=32s\": getting credentials: exec: fork/exec /opt/acme/acme-auth: no such file or directory"` +
			"\n" + `Unable to connect to the server: getting credentials: exec: fork/exec /opt/acme/acme-auth: no such file or directory`,
	} {
		for surface, verr := range map[string]*view.Error{
			"forward": kubectlFailed("homelab-pg", homelab, stderr),
			"secret":  secretFailed("homelab-pg", "databases", "pg-creds", stderr),
			"listing": listFailed("namespaces in homelab", stderr),
		} {
			if verr.Code != "tunnel.credential.missing" {
				t.Errorf("%s/%s: code = %s, want tunnel.credential.missing", exe, surface, verr.Code)
			}
			if !strings.Contains(verr.Message, exe+", the context's credential plugin") {
				t.Errorf("%s/%s: message %q does not name the tool", exe, surface, verr.Message)
			}
		}
	}
}

// A cancelled context ends the forward the way Close does: SIGTERM to the
// group, which kubectl answers by telling the API server the forward is
// over — not os/exec's default SIGKILL of the leader alone, which nothing
// can answer. The fake traps TERM and leaves a mark, which SIGKILL could
// never produce.
func TestCancellingTheContextTerminatesTheForwardGracefully(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "terminated")
	fakeKubectl(t, "trap 'echo yes > "+marker+"; exit 0' TERM\n"+
		"echo 'Forwarding from 127.0.0.1:54321 -> 5432'\n"+
		"while :; do sleep 1; done\n")
	ctx, cancel := context.WithCancel(context.Background())
	tun, verr := Open(ctx, "homelab-pg", Target{Kube: homelab})
	if verr != nil {
		t.Fatal(verr.Message)
	}
	defer tun.Close()
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("kubectl was not sent SIGTERM within five seconds of the context ending")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
