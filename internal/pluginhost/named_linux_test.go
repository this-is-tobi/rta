package pluginhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A path named for the artifact is allowed read only, less what of it is a
// credential location the artifact was not given; and the CA bundle rta hands
// every plugin by name is readable where it is.
func TestTheStrictPolicyReadsANamedPathLessWhatIsNotGiven(t *testing.T) {
	named := tree(t, "ca.pem", "gcloud/key.json")
	gcloud := filepath.Join(named, "gcloud")
	bundle := filepath.Join(tree(t, "corp.pem"), "corp.pem")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SSL_CERT_FILE", bundle)
	t.Setenv("SSL_CERT_DIR", "")
	policy := strictPolicy(DenySet{Strict: true, Named: []string{named}, NoRead: []string{gcloud}}, "/bin/sh")
	ro := flagged(policy, "--ro")
	if !slices.Contains(ro, filepath.Join(named, "ca.pem")) || !slices.Contains(ro, bundle) {
		t.Errorf("the policy does not allow the named path's CA and the env's bundle: %q", ro)
	}
	if slices.Contains(ro, named) || slices.ContainsFunc(ro, func(p string) bool { return inside(p, gcloud) }) {
		t.Errorf("the policy allows the named path whole, or the credential location inside it: %q", ro)
	}
	if slices.Contains(flagged(policy, "--rw"), named) {
		t.Errorf("a named path is writable: %q", policy)
	}
}

// The shim holds it for real: a shell under the policy reads the CA in the
// named path, and is refused the credential location inside it and a file
// beside the path that nobody named.
func TestTheShimReadsANamedPathAndNothingBesideIt(t *testing.T) {
	if landlockABI() < 1 {
		t.Skip("this kernel has no Landlock")
	}
	named := tree(t, "ca.pem", "gcloud/key.json")
	gcloud := filepath.Join(named, "gcloud")
	beside := filepath.Join(tree(t, "secret"), "secret")
	if err := os.WriteFile(filepath.Join(gcloud, "key.json"), []byte("hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beside, []byte("hunter3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", "")
	policy := strictPolicy(DenySet{Strict: true, Named: []string{named}, NoRead: []string{gcloud}}, "/bin/sh")
	script := "cat " + filepath.Join(named, "ca.pem") + "; cat " + filepath.Join(gcloud, "key.json") + "; cat " + beside
	args := append(append([]string{ConfineCommand}, policy...), "--", "/bin/sh", "-c", script)
	out, _ := exec.Command(os.Args[0], args...).CombinedOutput() //nolint:gosec // the test binary, as the shim
	if !strings.Contains(string(out), "ca.pem") {
		t.Errorf("the confined shell could not read the named path's CA:\n%s", out)
	}
	if strings.Contains(string(out), "hunter2") || strings.Contains(string(out), "hunter3") {
		t.Errorf("the confined shell read past the named path:\n%s", out)
	}
}
