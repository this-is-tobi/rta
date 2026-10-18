package http

import (
	"slices"
	"testing"
)

// The order inputs are declared in is the order a form asks them in. The body
// was declared after every credential and the timeout, so `data` was the ninth
// box of http.post's form and eight enters from the url.
func TestTheBodyIsAskedBeforeTheCredentials(t *testing.T) {
	want := map[string][]string{
		"http.post": {"url", "header", "data", "data-file", "bearer", "basic", "bearer-file", "basic-file", "local-network", "timeout"},
		"http.put":  {"url", "header", "data", "data-file", "bearer", "basic", "bearer-file", "basic-file", "local-network", "timeout"},
		"http.get":  {"url", "header", "bearer", "basic", "bearer-file", "basic-file", "local-network", "timeout"},
	}
	for _, c := range Plugin().Capabilities {
		names, ok := want[c.ID]
		if !ok {
			continue
		}
		var got []string
		for _, f := range c.Inputs {
			got = append(got, f.Name)
		}
		if !slices.Equal(got, names) {
			t.Errorf("%s asks %v, want %v", c.ID, got, names)
		}
	}
}
