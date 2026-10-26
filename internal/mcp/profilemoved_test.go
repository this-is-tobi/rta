package mcp

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/grant"
)

// twoProfiles with staging aimed somewhere else: the one edit a grant's pin
// exists to notice.
var stagingMoved = strings.Replace(twoProfiles, "host: staging.internal", "host: elsewhere.internal", 1)

// A call parks with the pin of the connection its profile named when it was
// asked about, and the question the operator answers carries that pin. The
// call resolved its connection again once the answer came, from the file as it
// was then, and nothing compared the two: a profile edited while the call
// waited sent the approved call to a connection nobody was asked about, while
// `--ttl` issued a grant pinned to the one they were.
func TestAnApprovalIsForTheConnectionItWasAskedAbout(t *testing.T) {
	f := newProfileFixture(t, twoProfiles, func(o *Options) {
		o.Consent, o.ConsentWait = true, 20*time.Second
		o.Active = func() string { return "" }
	})
	asked := make(chan consent.Request, 1)
	over := t.Context()
	go func() {
		for over.Err() == nil {
			pending, err := consent.Pending()
			if err == nil && len(pending) > 0 {
				asked <- pending[0]
				// The operator edits the profile, then answers the question
				// that was on their screen before the edit.
				if err := writeFile(f.dir+"/config.yaml", stagingMoved); err != nil {
					t.Error(err)
				}
				if err := consent.Decide(pending[0].ID, true, "test"); err != nil {
					t.Errorf("Decide: %v", err)
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	res := f.call(t, map[string]any{"profile": "staging", "sql": "select 1"})
	select {
	case req := <-asked:
		if req.Pin != f.pin("staging", "pg") {
			t.Fatalf("the question carried pin %q, not the connection staging named when it was asked", req.Pin)
		}
	default:
		t.Fatal("nothing was parked, so this proves nothing")
	}
	assertCode(t, res, "core.profile.moved")
	if *f.sawHost != "" {
		t.Fatalf("the approved call ran against %q, a connection nobody was asked about", *f.sawHost)
	}
	if text := contentText(t, res); strings.Contains(text, "elsewhere.internal") || strings.Contains(text, "staging.internal") {
		t.Errorf("the refusal tells the agent where the connection goes: %s", text)
	}
	rec := lastRecord(t)
	if rec.Outcome != agentlog.Refused || rec.Code != "core.profile.moved" {
		t.Errorf("the record says outcome %q code %q", rec.Outcome, rec.Code)
	}
	if !strings.Contains(rec.Note, "edited") {
		t.Errorf("the record does not tell the operator why their answer was not acted on: note %q", rec.Note)
	}
}

// A profile left as it was is not a moved one: the approved call runs, once,
// and reads the file once before it parks and once after the answer.
func TestAnApprovalForAnUnchangedConnectionRuns(t *testing.T) {
	var reads atomic.Int32
	f := newProfileFixture(t, twoProfiles, func(o *Options) {
		o.Consent, o.ConsentWait = true, 20*time.Second
		o.Active = func() string { return "" }
		o.Reload = func() (config.Config, error) {
			reads.Add(1)
			return config.Load()
		}
	})
	_ = answerWhenAsked(t, true)
	reads.Store(0)

	res := f.call(t, map[string]any{"profile": "staging", "sql": "select 1"})
	if res.IsError {
		t.Fatalf("an approved call on an unchanged connection was refused: %s", contentText(t, res))
	}
	if *f.sawHost != "staging.internal" {
		t.Errorf("host = %q, want staging's", *f.sawHost)
	}
	if n := reads.Load(); n != 2 {
		t.Errorf("the call read the config %d times, want 2: once before it parked and once after the answer", n)
	}
}

// The gate's pin and the connection the call is filled from were two reads of
// the file, so an edit landing between them — an agent with a shell flipping
// a host in a loop is enough — let a grant pinned to staging's connection run
// a call on whatever staging named a moment later. One read per call means
// whichever moment the edit lands at, the call either runs on the connection
// its pin was judged against or is refused for the mismatch.
func TestAGrantedCallRunsOnTheConnectionItsPinWasJudgedAgainst(t *testing.T) {
	moved, err := config.Parse([]byte(stagingMoved))
	if err != nil {
		t.Fatal(err)
	}
	var reads, flipAt atomic.Int32
	f := newProfileFixture(t, twoProfiles, func(o *Options) {
		o.Active = func() string { return "" }
		o.Reload = func() (config.Config, error) {
			if reads.Add(1) > flipAt.Load() {
				return moved, nil
			}
			return config.Load()
		}
	})
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "pg", Profile: "staging", ProfilePin: f.pin("staging", "pg"), Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}

	ran := 0
	for at := int32(0); at <= 6; at++ {
		reads.Store(0)
		flipAt.Store(at)
		*f.sawHost = ""
		res := f.call(t, map[string]any{"profile": "staging", "sql": "select 1"})
		switch *f.sawHost {
		case "elsewhere.internal":
			t.Fatalf("with the edit after read %d, a grant pinned to staging.internal ran a call on elsewhere.internal", at)
		case "staging.internal":
			ran++
		case "":
			assertCode(t, res, "core.grant.required")
		}
		if n := reads.Load(); n != 1 {
			t.Errorf("with the edit after read %d, the call read the config %d times, want 1", at, n)
		}
	}
	if ran == 0 {
		t.Fatal("no call ran at all, so this proves nothing")
	}
}

// A call that names no profile stamps nothing, and no longer pays a parse of
// the file to find that out.
func TestACallReadsTheConfigOnce(t *testing.T) {
	var reads atomic.Int32
	f := newProfileFixture(t, twoProfiles, func(o *Options) {
		o.Active = func() string { return "" }
		o.Reload = func() (config.Config, error) {
			reads.Add(1)
			return config.Load()
		}
	})
	reads.Store(0)
	if res := f.callOther(t); res.IsError {
		t.Fatalf("an unprofiled read was refused: %s", contentText(t, res))
	}
	if n := reads.Load(); n != 1 {
		t.Errorf("an unprofiled call read the config %d times, want 1", n)
	}
}
