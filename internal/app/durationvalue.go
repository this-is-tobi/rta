package app

import "github.com/this-is-tobi/rta/pkg/plugin"

// durationValue is how an input of type Duration is given on the command line:
// the text the caller wrote, held as text because that is what every surface
// and the wire carry, and refused at parse time when it is no length of time.
//
// Not pflag's own Duration, which would hand the plugin a time.Duration where
// the contract is text, and which stops at the hour: a capability declaring
// `1d` as its default would print a flag its own help text cannot be typed
// back into. Parsed with the grammar the declaration is held to
// (plugin.ParseDuration), so a bare number is refused here as it is on every
// other surface, and the refusal reaches refuseFlagValue under the type's own
// name, with the capability's range beside it.
type durationValue struct{ value string }

func newDurationValue(def string) *durationValue { return &durationValue{value: def} }

func (d *durationValue) Set(raw string) error {
	if _, err := plugin.ParseDuration(raw); err != nil {
		return err
	}
	d.value = raw
	return nil
}

// Type is "duration", the name pflag's own gives its Duration flag, so a help
// page, a completion and refuseFlagValue read the flag as they read that one.
func (d *durationValue) Type() string { return "duration" }

func (d *durationValue) String() string { return d.value }
