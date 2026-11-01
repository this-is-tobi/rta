package pluginhost

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
)

// refusingStub is a plugin process whose every call fails with e.
type refusingStub struct {
	rtav1.PluginServiceClient
	e *rtav1.Error
}

func (s refusingStub) Call(context.Context, *rtav1.CallRequest, ...grpc.CallOption) (*rtav1.CallResponse, error) {
	return &rtav1.CallResponse{Result: &rtav1.CallResponse_Error{Error: s.e}}, nil
}

// Through the call as a person makes it, against the policy a launch is
// given: a CA file under the home directory, which strict leaves out, is named
// as the setting's refusal; one under /etc, which it allows, was refused by
// something else and keeps the plugin's own hint.
func TestAStrictPluginRefusedAFileOffItsPolicySaysWhichSetting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const own = "--ca-file is a path on this machine, read by rta rather than by the server"
	for _, tc := range []struct {
		path  string
		named bool
	}{
		{filepath.Join(home, "ca.pem"), true},
		{"/etc/passwd", false},
	} {
		stub := refusingStub{e: &rtav1.Error{Code: "redis.tls.ca.unreadable",
			Message: "open " + tc.path + ": permission denied", Hint: own}}
		c := &Client{stub: stub, deny: DenySet{Strict: true}}
		dst := plugin.Capability{ID: "redis.overview"}
		c.attach(&dst, &rtav1.Capability{})
		_, err := dst.Run(context.Background(), plugin.NewRequest(nil, false, false).WithSurface(plugin.SurfaceCLI))
		var verr *view.Error
		if !errors.As(err, &verr) {
			t.Fatalf("%s: the call failed with %v", tc.path, err)
		}
		if named := strings.Contains(verr.Hint, "`confine: strict` keeps redis from opening "+tc.path); named != tc.named ||
			(!tc.named && verr.Hint != own) {
			t.Errorf("%s: the hint reads %q", tc.path, verr.Hint)
		}
	}
}
