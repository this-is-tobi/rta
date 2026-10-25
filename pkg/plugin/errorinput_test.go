package plugin

import (
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// An error names the input it is about by the name the capability declares it
// under, and a surface acts on no other: a plugin's error crosses the wire
// holding whatever its handler wrote.
func TestAnErrorNamesOnlyAnInputTheCapabilityDeclares(t *testing.T) {
	c := Capability{ID: "weather.station.show", Inputs: []Field{{Name: "city", Type: String}}}
	if f, ok := c.ErrorInput(view.Errorf("weather.nosuchcity", "no station").WithInput("city")); !ok || f.Name != "city" {
		t.Errorf("a declared input = %q, %v", f.Name, ok)
	}
	for _, verr := range []*view.Error{
		nil,
		view.Errorf("weather.down", "the service did not answer"),
		view.Errorf("weather.nosuchcity", "no station").WithInput("town"),
	} {
		if f, ok := c.ErrorInput(verr); ok {
			t.Errorf("%+v names %q, which nothing declares", verr, f.Name)
		}
	}
}
