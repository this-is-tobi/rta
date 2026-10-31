package sdktest

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// sender is a write whose dry run either describes the call or, sends set,
// makes it anyway: the defect RuleDryRun exists for, aimed at a service.
func sender(sends bool) plugin.Capability {
	return plugin.Capability{
		ID: "demo.item.send", Summary: "send an item", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "address", Type: plugin.String, Required: true}},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			if req.DryRun && !sends {
				return view.Text{Body: "would send the item to " + req.String("address")}, nil
			}
			c, err := net.Dial("tcp", req.String("address"))
			if err != nil {
				return nil, view.Errorf("demo.item.unreachable", "%v", err)
			}
			_ = c.Close()
			return view.Text{Body: "sent"}, nil
		},
	}
}

// Pointed at 127.0.0.1:1, a dry run that sent its request failed with a
// refused connection, which is a coded error, which the rule took for an
// answer: the suite passed it. Pointed at the address Nowhere gives, the
// connection itself is what fails it, while a dry run that only describes the
// call passes, and a read that connects is a read's business.
func TestADryRunThatConnectsToNowhereFailsTheSuite(t *testing.T) {
	for _, c := range []struct {
		label string
		sends bool
		fails bool
	}{
		{"a dry run that sends", true, true},
		{"a dry run that describes", false, false},
	} {
		dir := t.TempDir()
		openNowhere(t, dir)
		inputs := map[string]map[string]any{"demo.item.send": {"address": Nowhere(dir)}}
		p := plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{ok(), sender(c.sends)}}
		rec := &recorder{}
		drive(rec, p, noConfig(), dir, inputs)
		caught := strings.Contains(rec.errText(), "connected to the address sdktest.Nowhere gives")
		if caught != c.fails {
			t.Errorf("%s: caught = %v, want %v:\n%s", c.label, caught, c.fails, rec.errText())
		}
	}
}

// Nowhere answers only for a run's own directory, and says how to call it
// when asked for another: an address that silently pointed nowhere would be
// 127.0.0.1:1 again.
func TestNowhereNamesTheRunItBelongsTo(t *testing.T) {
	defer func() {
		r := recover()
		if s, _ := r.(string); !strings.Contains(s, "sdktest.WithInputs") {
			t.Errorf("Nowhere outside a run: recovered %v, want a panic that says how to call it", r)
		}
	}()
	Nowhere(t.TempDir())
}
