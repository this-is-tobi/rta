package plugin

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// The flags that state a connection for one call are the host's on a
// capability a forward can fill, and a plugin's own word anywhere else.

func connectionCap(extra ...Field) Capability {
	return Capability{
		ID: "db.status", Summary: "status", Safety: Read,
		Inputs: append([]Field{{Name: "address", Type: String, Default: "localhost:5432", Config: "address",
			Local: true, Endpoint: EndpointAddress, Help: "address"}}, extra...),
		Run: func(context.Context, Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
	}
}

func TestAnInputTheHostGivesAFlagToIsRefusedWhereAForwardCanFillTheCapability(t *testing.T) {
	for _, name := range []string{"kube", "secret", "secrets-from"} {
		c := connectionCap(Field{Name: name, Type: String, Help: "a value"})
		err := Plugin{Name: "db", Summary: "db", Capabilities: []Capability{c}}.Validate()
		if err == nil || !strings.Contains(err.Error(), "reserved by the host") {
			t.Errorf("input %q beside an endpoint role: %v, want it reserved", name, err)
		}
	}
}

func TestTheSameNamesAreOrdinaryOnACapabilityNoForwardCanFill(t *testing.T) {
	for _, name := range []string{"kube", "secret", "secrets-from"} {
		c := Capability{
			ID: "db.status", Summary: "status", Safety: Read,
			Inputs: []Field{{Name: name, Type: String, Help: "a value"}},
			Run:    func(context.Context, Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		}
		if err := (Plugin{Name: "db", Summary: "db", Capabilities: []Capability{c}}).Validate(); err != nil {
			t.Errorf("input %q on a capability with no endpoint input: %v", name, err)
		}
	}
}
