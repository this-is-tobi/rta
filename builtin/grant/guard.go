package grant

import (
	"context"
	"fmt"

	core "github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/guard"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func runGuardOn(_ context.Context, req plugin.Request) (view.View, error) {
	if req.Surface() == plugin.SurfaceMCP {
		return nil, view.Errorf("grant.human", "the guard can only be enabled by a person")
	}
	if guard.Enabled() {
		return nil, view.Errorf("core.guard.exists", "the guard is already enabled").
			WithHint("`rta grant guard off` first, if you mean to rotate the passphrase")
	}
	held, verr := core.Load()
	if verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would enable the guard: issuing or renewing a grant "+
			"then requires the passphrase, and the %d grant(s) currently held are cleared — "+
			"they were issued without one", len(held))}, nil
	}
	pass, verr := guard.PromptSecret(req, true)
	if verr != nil {
		return nil, verr
	}
	// Clear first, enable second: a crash between the two leaves grants
	// cleared and the guard off, which is inconvenient and safe. The other
	// order leaves the guard on over unsigned rows, which loadAll would then
	// refuse as forgery — an alarm raised by the recovery, seal.go's own
	// phrase for the wrong shape.
	//
	// Cleared rather than migrated, and legacy() owns the argument: a
	// migration that re-signs what it finds is the same hole with more
	// steps, because the rows being blessed are exactly the rows whose
	// authorship the guard exists to pin. Grants are a day at most; what
	// this costs is re-issued in minutes, with the passphrase.
	if verr := core.Mutate(func([]core.Grant) ([]core.Grant, bool) {
		return nil, true
	}); verr != nil {
		return nil, verr
	}
	if _, verr := guard.Enable(pass); verr != nil {
		return nil, verr
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "guard", Value: "on — issuing or renewing a grant now asks for the passphrase"},
		{Key: "key", Value: guard.Fingerprint()},
		{Key: "cleared", Value: fmt.Sprintf("%d grant(s) issued before the guard", len(held))},
		{Key: "forgotten?", Value: "rm " + guard.Path() + " and `rta grant revoke --all` start clean"},
	}}, nil
}

func runGuardOff(_ context.Context, req plugin.Request) (view.View, error) {
	if req.Surface() == plugin.SurfaceMCP {
		return nil, view.Errorf("grant.human", "the guard can only be disabled by a person")
	}
	if !guard.Enabled() {
		return nil, view.Errorf("core.guard.off", "the guard is not enabled")
	}
	held, verr := core.Load()
	if verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would disable the guard after checking the passphrase, "+
			"clearing the %d grant(s) it signed", len(held))}, nil
	}
	pass, verr := guard.PromptSecret(req, false)
	if verr != nil {
		return nil, verr
	}
	// Proven before anything is touched: a wrong passphrase must cost
	// nothing, and especially not the grants.
	if _, verr := guard.Unlock(pass); verr != nil {
		return nil, verr
	}
	// Clear while the guard still stands, then remove it — the mirror of
	// enable's ordering, with the same crash story: dying between the two
	// leaves the guard on over an empty file, which a retry finishes.
	if verr := core.Mutate(func([]core.Grant) ([]core.Grant, bool) {
		return nil, true
	}); verr != nil {
		return nil, verr
	}
	if verr := guard.Disable(pass); verr != nil {
		return nil, verr
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "guard", Value: "off — grants issue without a passphrase again"},
		{Key: "cleared", Value: fmt.Sprintf("%d grant(s) the guard had signed", len(held))},
	}}, nil
}

func runGuardStatus(_ context.Context, req plugin.Request) (view.View, error) {
	if req.Surface() == plugin.SurfaceMCP {
		return nil, view.Errorf("grant.human", "the guard's status is for the person at the terminal")
	}
	if !guard.Enabled() {
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "guard", Value: "off — any process running as you can issue a grant"},
			{Key: "enable", Value: "rta grant guard on"},
		}}, nil
	}
	held, verr := core.Load()
	if verr != nil {
		return nil, verr
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "guard", Value: "on — issuing or renewing a grant asks for the passphrase"},
		{Key: "since", Value: guard.Created().Local().Format("2006-01-02 15:04")},
		{Key: "key", Value: guard.Fingerprint()},
		{Key: "grants", Value: fmt.Sprintf("%d held, all guard-signed", len(held))},
	}}, nil
}
