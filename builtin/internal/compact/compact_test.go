package compact

import (
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

func request(surface plugin.Surface, values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, false).WithSurface(surface)
}

func TestACommandLineOnATerminalIsCompactUnlessItIsAskedForDetail(t *testing.T) {
	defer Pretend(true)()
	if !For(request(plugin.SurfaceCLI, nil)) {
		t.Error("a person at a terminal is shown the compact listing")
	}
	if For(request(plugin.SurfaceCLI, map[string]any{"detail": true})) {
		t.Error("--detail is every field, on a terminal too")
	}
}

func TestAPipeIsGivenEveryField(t *testing.T) {
	defer Pretend(false)()
	if For(request(plugin.SurfaceCLI, nil)) {
		t.Error("output that is not a terminal is a program's, and a program is promised every field")
	}
}

func TestTheTUIIsCompactWhateverDetailSays(t *testing.T) {
	defer Pretend(false)()
	for _, detail := range []bool{false, true} {
		if !For(request(plugin.SurfaceTUI, map[string]any{"detail": detail})) {
			t.Errorf("the TUI with detail=%v is not compact: its whole-screen result carries detail by default", detail)
		}
	}
}

func TestNothingElseIsCompact(t *testing.T) {
	defer Pretend(true)()
	for _, sf := range []plugin.Surface{plugin.SurfaceMCP, plugin.SurfaceUnknown} {
		if For(request(sf, nil)) {
			t.Errorf("surface %v is compact", sf)
		}
	}
}

func TestAFormatAskedForAtATerminalKeepsEveryField(t *testing.T) {
	defer Pretend(true)()
	for _, args := range [][]string{
		{"agent", "log", "-o", "json"},
		{"agent", "log", "--output", "yaml"},
		{"agent", "log", "--output=csv"},
		{"agent", "log", "-o=md"},
		{"agent", "log", "-ojson"},
		{"-o", "JSON", "agent", "log"},
	} {
		commandLine = func() []string { return args }
		if For(request(plugin.SurfaceCLI, nil)) {
			t.Errorf("%v is a machine format and was drawn compact", args)
		}
	}
	for _, args := range [][]string{
		{"agent", "log"},
		{"agent", "log", "-o", "pretty"},
		{"agent", "log", "--limit", "5"},
		{"agent", "log", "--", "-o", "json"},
	} {
		commandLine = func() []string { return args }
		if !For(request(plugin.SurfaceCLI, nil)) {
			t.Errorf("%v asks for no machine format and was not drawn compact", args)
		}
	}
}

func TestTheEnvironmentNamesAFormatAndTheCommandLineOverridesIt(t *testing.T) {
	defer Pretend(true)()
	envOutput = func() string { return "json" }
	if For(request(plugin.SurfaceCLI, nil)) {
		t.Error("RTA_OUTPUT=json is a machine format")
	}
	commandLine = func() []string { return []string{"agent", "log", "-o", "pretty"} }
	if !For(request(plugin.SurfaceCLI, nil)) {
		t.Error("-o pretty on the command line wins over the environment")
	}
}
