package all

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A config key that several capabilities of one plugin read is one setting to the person
// who writes it, so it has to mean the same thing to each. `plugins.net.timeout` once drove
// five commands at once, an overall timeout for ping and a limit per port for port, with a
// default of 10 for one and 2 for the other: setting it to speed one up changed four others,
// and nothing in the key said which. A key whose capabilities differ in type, default or
// range is named for one capability (`ping.timeout`), as `ping.count` is.
func TestAConfigKeyReadByTwoCapabilitiesMeansTheSameToBoth(t *testing.T) {
	reg, err := Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	type reading struct {
		capability string
		field      plugin.Field
	}
	for _, p := range reg.Plugins() {
		byKey := map[string][]reading{}
		for _, c := range p.Capabilities {
			for _, f := range c.Inputs {
				if f.Config != "" {
					byKey[f.Config] = append(byKey[f.Config], reading{c.ID, f})
				}
			}
		}
		for key, readers := range byKey {
			for _, r := range readers[1:] {
				first := readers[0]
				if r.field.Type != first.field.Type || !reflect.DeepEqual(r.field.Default, first.field.Default) ||
					r.field.Min != first.field.Min || r.field.Max != first.field.Max {
					t.Errorf("plugins.%s.%s is read by %s (%s) and by %s (%s): name it for one capability",
						p.Name, key, first.capability, shape(first.field), r.capability, shape(r.field))
				}
			}
		}
	}
}

func shape(f plugin.Field) string {
	return fmt.Sprintf("default %v, from %v to %v", f.Default, f.Min, f.Max)
}
