package stdio

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// restore puts the process's real stdin back, since Claim mutates a package
// variable the whole binary shares.
func restore(t *testing.T) {
	t.Helper()
	savedStdin, savedClaimed := os.Stdin, claimed
	claimed = nil // Claim is idempotent, so a prior test's claim would win
	t.Cleanup(func() { os.Stdin, claimed = savedStdin, savedClaimed })
}

// The property the whole package exists for: after Claim, the caller holds
// the stream and os.Stdin — which is what go-plugin copies into every child
// — reads nothing.
func TestClaimHandsBackTheStreamAndLeavesChildrenNothing(t *testing.T) {
	restore(t)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	os.Stdin = r

	if err := Claim(); err != nil {
		t.Fatal(err)
	}
	if Real() != r {
		t.Error("Real is not the original stdin")
	}
	if os.Stdin == r {
		t.Fatal("os.Stdin still points at the caller's stream, so a plugin would read it")
	}

	// Anything written to the real stream reaches the claimer...
	go func() {
		_, _ = w.Write([]byte("request line\n"))
		_ = w.Close()
	}()
	var got bytes.Buffer
	if _, err := io.Copy(&got, Real()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.String(), "request line") {
		t.Errorf("the claimed stream lost data: %q", got.String())
	}

	// ...and os.Stdin is /dev/null, which reads EOF immediately rather than
	// blocking or stealing bytes.
	var leaked bytes.Buffer
	if _, err := io.Copy(&leaked, os.Stdin); err != nil {
		t.Fatalf("reading the replacement stdin: %v", err)
	}
	if leaked.Len() != 0 {
		t.Errorf("os.Stdin yielded %q, want nothing", leaked.String())
	}
}

// The end-to-end shape: a child launched the way go-plugin launches one —
// cmd.Stdin = os.Stdin, set after the parent has already configured the
// command — must read nothing.
//
// This is the test that would have caught the original defect. A confined
// plugin took all eight tools/call lines whole and the parent read zero,
// because the plugin's fd 0 was the agent's request stream.
func TestAChildLaunchedTheWayGoPluginLaunchesOneReadsNothing(t *testing.T) {
	restore(t)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	os.Stdin = r
	go func() {
		_, _ = w.Write([]byte("secret protocol traffic\n"))
		_ = w.Close()
	}()

	if err := Claim(); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("/bin/cat")
	// Exactly what go-plugin does, and the reason there is no call-site fix:
	// it assigns this itself, after whatever the host set.
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running the child: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("a child read %q from the caller's stream", out)
	}

	// And the parent still has every byte.
	var got bytes.Buffer
	if _, err := io.Copy(&got, Real()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.String(), "secret protocol traffic") {
		t.Errorf("the parent lost data to the child: %q", got.String())
	}
}

// Closing the transport must not close the process's stdout: the MCP session
// ends and the error explaining why still has to be printable.
func TestWriterSwallowsClose(t *testing.T) {
	var buf bytes.Buffer
	w := Writer(&buf)
	if _, err := w.Write([]byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close returned %v", err)
	}
	if _, err := w.Write([]byte(" two")); err != nil {
		t.Fatalf("writing after Close failed: %v", err)
	}
	if buf.String() != "one two" {
		t.Errorf("buffer = %q", buf.String())
	}
}

// Real answers the true stream before Claim has run, so a surface that asks
// for input works the same in a test, in a library embedding rta, and in the
// binary. Without this, every kv passphrase prompt and the whole TUI would
// depend on main having run first.
func TestRealIsTheTrueStreamBeforeClaim(t *testing.T) {
	restore(t)
	r, _, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	os.Stdin = r
	if Real() != r {
		t.Error("before Claim, Real must be os.Stdin itself")
	}
}

// Claim twice must not lose the real stream behind a second /dev/null. main
// calls it once, but a second caller appearing is exactly the kind of thing
// that happened to this package before, and the failure would be silent:
// every prompt and the TUI reading nothing, with no error anywhere.
func TestClaimIsIdempotent(t *testing.T) {
	restore(t)
	r, _, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	os.Stdin = r

	if err := Claim(); err != nil {
		t.Fatal(err)
	}
	if err := Claim(); err != nil {
		t.Fatal(err)
	}
	if Real() != r {
		t.Errorf("a second Claim lost the real stream: Real is %v", Real().Name())
	}
}
