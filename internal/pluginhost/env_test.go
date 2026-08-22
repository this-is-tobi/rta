package pluginhost

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// go-plugin's SkipHostEnv defaults to false, so the out-of-the-box behaviour
// is that a plugin inherits the host's entire environment — every RTA_*
// variable, every cloud credential the user exported into that shell, every
// token their direnv set. A plugin that never touches the filesystem could
// exfiltrate an AWS session by starting up.
func TestNothingSecretReachesAPluginsEnvironment(t *testing.T) {
	host := []string{
		"PATH=/usr/bin",
		"HOME=/home/u",
		"TMPDIR=/private/var/t",
		"LANG=en_GB.UTF-8",
		"LC_TIME=en_GB.UTF-8",
		"TZ=Europe/Paris",
		"SSL_CERT_FILE=/etc/ssl/corp.pem",
		// None of these may cross.
		"RTA_DATA_DIR=/home/u/.local/share/rta",
		"RTA_CONFIG=/home/u/.config/rta/config.yaml",
		"AWS_SECRET_ACCESS_KEY=wJalrXUt",
		"AWS_SESSION_TOKEN=FQoGZXIvYXdz",
		"GITHUB_TOKEN=ghp_xxx",
		"OPENAI_API_KEY=sk-xxx",
		"KUBECONFIG=/home/u/.kube/config",
		"DATABASE_URL=postgres://u:p@h/db",
		"SSH_AUTH_SOCK=/private/tmp/agent.sock",
	}
	got := strings.Join(childEnv(host), "\n")

	for _, want := range []string{
		"PATH=/usr/bin", "HOME=/home/u", "TMPDIR=/private/var/t",
		"LANG=en_GB.UTF-8", "LC_TIME=en_GB.UTF-8", "TZ=Europe/Paris",
		"SSL_CERT_FILE=/etc/ssl/corp.pem",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%s did not cross, and it is on the allowlist for a reason", want)
		}
	}
	for _, leaked := range []string{
		"RTA_", "AWS_", "GITHUB_TOKEN", "OPENAI_API_KEY", "KUBECONFIG",
		"DATABASE_URL", "SSH_AUTH_SOCK",
	} {
		if strings.Contains(got, leaked) {
			t.Errorf("%s reached the plugin:\n%s", leaked, got)
		}
	}
}

// TMPDIR is on the allowlist as a security measure rather than a convenience,
// and it is the one entry most likely to be deleted by somebody tidying the
// list. Dropping it moves every plugin's os.CreateTemp("", …) from a per-user
// 0700 directory to macOS's mode-1777 shared /tmp.
func TestTmpdirIsCarriedDeliberately(t *testing.T) {
	got := childEnv([]string{"TMPDIR=/private/var/folders/xy/T/"})
	if len(got) != 1 {
		t.Fatalf("TMPDIR was dropped: %v", got)
	}
}

// An empty value and an absent name mean different things to a surprising
// number of programs, so filtering must not normalise one into the other.
func TestAnEmptyAllowedValueCrossesAsEmpty(t *testing.T) {
	got := childEnv([]string{"LANG="})
	if len(got) != 1 || got[0] != "LANG=" {
		t.Errorf("childEnv = %v, want LANG= carried through as empty", got)
	}
}

// The end-to-end half: the command rta actually builds, run for real, asked
// what it can see. A unit test over childEnv proves the filter works; this
// proves the filter is wired into the spawn.
func TestTheLaunchedCommandCarriesOnlyTheAllowlist(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("AWS_SECRET_ACCESS_KEY", "wJalrXUt")

	env, err := exec.LookPath("env")
	if err != nil {
		t.Skip("no env(1)")
	}
	id, err := Identify(env)
	if err != nil {
		t.Fatal(err)
	}
	deny, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}

	cmd := buildCmd(id, deny, nil)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running the built command: %v", err)
	}
	for _, leaked := range []string{"RTA_DATA_DIR", "AWS_SECRET_ACCESS_KEY"} {
		if strings.Contains(string(out), leaked) {
			t.Errorf("%s reached a launched process:\n%s", leaked, out)
		}
	}
	// PATH has to be there or a plugin that shells out finds nothing.
	if !strings.Contains(string(out), "PATH=") {
		t.Errorf("PATH did not reach the process:\n%s", out)
	}
}

// The allowlist is names, not values, and a name that merely starts with an
// allowed one must not slip through. "PATHOLOGICAL=x" is not PATH.
func TestPrefixCollisionsDoNotSlipThrough(t *testing.T) {
	got := strings.Join(childEnv([]string{
		"PATH=/usr/bin",
		"PATHOLOGICAL=leak",
		"HOMEBREW_GITHUB_API_TOKEN=ghp_x",
		"TZDATA=leak",
	}), "\n")
	if strings.Contains(got, "PATHOLOGICAL") || strings.Contains(got, "HOMEBREW") ||
		strings.Contains(got, "TZDATA") {
		t.Errorf("a name that merely shares a prefix crossed:\n%s", got)
	}
	if !strings.Contains(got, "PATH=/usr/bin") {
		t.Error("PATH itself was dropped")
	}
}

// Environ is childEnv over the real environment; a test that only exercised
// the injectable form would miss a wiring mistake in the one caller.
func TestEnvironFiltersTheRealEnvironment(t *testing.T) {
	t.Setenv("RTA_SECRET_PROBE", "should-not-cross")
	for _, kv := range Environ() {
		if strings.HasPrefix(kv, "RTA_") {
			t.Errorf("Environ carried %q", kv)
		}
	}
	// Sanity: the variable really is in the host environment, so the check
	// above is testing the filter rather than an empty set.
	if os.Getenv("RTA_SECRET_PROBE") != "should-not-cross" {
		t.Fatal("the probe variable was not set, so this test proved nothing")
	}
}
