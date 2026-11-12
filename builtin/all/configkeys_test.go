package all

import "testing"

// Every built-in's config keys follow the rules every plugin's do
// (plugin.ValidateKeys): a key several capabilities read means one thing to
// each, a dotted key is one capability's own, and a renamed key is one
// nothing reads. The built-ins are held to them here, as a plugin is by Serve
// and sdktest, rather than by the registry, which takes a plugin built on an
// older SDK as it is.
func TestEveryBuiltInsConfigKeysFollowTheRulesEveryPluginsDo(t *testing.T) {
	reg, err := Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range reg.Plugins() {
		if err := p.ValidateKeys(); err != nil {
			t.Errorf("%s: %v", p.Name, err)
		}
	}
}
