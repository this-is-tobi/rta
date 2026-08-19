package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func TestExplainCatalog(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, reg, "explain")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sys.cpu", "cert.inspect", "net.ping", "read"} {
		if !strings.Contains(out, want) {
			t.Errorf("catalog missing %q", want)
		}
	}
}

func TestExplainCard(t *testing.T) {
	reg, _ := NewRegistry()
	out, _, err := run(t, reg, "explain", "net.port")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"net.port", "rta net port <host>", "net_port", "input:ports"} {
		if !strings.Contains(out, want) {
			t.Errorf("card missing %q:\n%s", want, out)
		}
	}
}

// The card is where a person or an agent is sent to find out how to invoke a
// capability, and it was the one surface that never mentioned --detail: the
// tool description advertises it in CLI syntax and the MCP schema publishes
// it as a boolean, so `rta explain sys.overview` described a capability whose
// richest view appeared not to exist. Asserted over the whole catalogue
// rather than one ID, because the disagreement was catalogue-wide.
func TestExplainCardShowsTheDetailFlag(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	detailed := 0
	for _, c := range reg.Capabilities() {
		if !c.Detailed {
			continue
		}
		detailed++
		out, _, err := run(t, reg, "explain", c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "--detail") {
			t.Errorf("%s: invocation omits --detail:\n%s", c.ID, out)
		}
		if !strings.Contains(out, "input:detail") {
			t.Errorf("%s: card does not say what --detail does:\n%s", c.ID, out)
		}
	}
	if detailed == 0 {
		t.Fatal("no Detailed capability in the catalogue: this test proves nothing")
	}
}

// …and it is the flag of a capability that has a detail view, not decoration
// on every card.
func TestExplainCardOmitsDetailWhereThereIsNone(t *testing.T) {
	reg, _ := NewRegistry()
	out, _, err := run(t, reg, "explain", "net.port")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "detail") {
		t.Errorf("net.port has no detail view but its card offers one:\n%s", out)
	}
}

func TestExplainSuggestsClosestMatch(t *testing.T) {
	reg, _ := NewRegistry()
	_, errOut, err := run(t, reg, "explain", "sys.cpuu")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "core.capability.unknown" {
		t.Fatalf("want core.capability.unknown, got %v", err)
	}
	if !strings.Contains(errOut, "sys.cpu") {
		t.Errorf("suggestion missing from stderr:\n%s", errOut)
	}
	if ExitCode(err) != 1 {
		t.Errorf("exit = %d, want 1", ExitCode(err))
	}
}

func TestDoctorReport(t *testing.T) {
	reg, _ := NewRegistry()
	out, _, err := run(t, reg, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"built-ins", "config", "exec plugins"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor missing %q:\n%s", want, out)
		}
	}
}
