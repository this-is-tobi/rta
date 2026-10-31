package agent

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A call parked on github.com written with a Cyrillic letter for its i was
// listed, shown and asked about as the host it imitates, although the name it
// resolves is xn--gthub-n2e.com: the queue, the detail page, the card and the
// question all draw a record through textclean.Record, which now says what the
// host resolves as. The letter is planted by code point: a source file may not
// hold a lookalike.
func TestACallOnALookalikeHostIsQueuedShownAndAskedWithTheNameItResolvesAs(t *testing.T) {
	isolate(t)
	host := "https://g" + string(rune(0x456)) + "thub.com/"
	const note = " (xn--gthub-n2e.com)"
	p, err := consent.Ask(consent.Call{
		Cap: "http.get", Safety: "read", Scopes: []string{host}, Agent: "probe",
		Args: map[string]any{"url": host}, Why: "no active grant for http.get " + host,
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)

	pending, err := run(t, "agent.pending", nil)
	if err != nil {
		t.Fatal(err)
	}
	table := pending.(view.Table)
	record := slices.IndexFunc(table.Columns, func(c view.Column) bool { return c.Name == "Record" })
	wouldDo := slices.IndexFunc(table.Columns, func(c view.Column) bool { return c.Name == "Would Do" })
	if record < 0 || wouldDo < 0 || len(table.Rows) != 1 {
		t.Fatalf("agent pending has no Record or Would Do column or not one row: %+v", table)
	}
	if got := table.Rows[0][record]; got != host+note {
		t.Errorf("agent pending shows the record as %q, want %q", got, host+note)
	}
	if got := table.Rows[0][wouldDo]; got != "url="+host+note {
		t.Errorf("agent pending shows what it would do as %q, want %q", got, "url="+host+note)
	}

	shown, err := run(t, "agent.show", map[string]any{"id": p.Request.ID})
	if err != nil {
		t.Fatal(err)
	}
	pairs := map[string]string{}
	for _, item := range shown.(view.Sections).Items {
		if kv, ok := item.View.(view.KeyValue); ok {
			for _, pair := range kv.Pairs {
				pairs[item.Key()+"/"+pair.Key] = pair.Value
			}
		}
	}
	for key, want := range map[string]string{"request/record": host + note, "arguments/url": host + note} {
		if pairs[key] != want {
			t.Errorf("agent show gives %s as %q, want %q", key, pairs[key], want)
		}
	}

	q := terminal(t, "n")
	if _, err := allowAt(t, nil, false, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"http.get " + host + note + " — probe asked", "url=" + host + note} {
		if !strings.Contains(q.card, want) {
			t.Errorf("the card does not say %q:\n%s", want, q.card)
		}
	}
	if want := "keep allowing http.get " + host + note + ", whatever"; !strings.Contains(approveQuestion(p.Request, "", "30m"), want) {
		t.Errorf("the --ttl question does not say %q: %q", want, approveQuestion(p.Request, "", "30m"))
	}
}

// A --ttl answer on a record that is a folder issues no grant, and says which
// record it declined to make one on: drawn as every other record is, with the
// name its host resolves as.
func TestTheFolderAnAnswerDeclinedToGrantOnSaysWhatItsHostResolvesAs(t *testing.T) {
	isolate(t)
	host := "https://g" + string(rune(0x456)) + "thub.com/"
	r := park(t, "http.get", host)
	v, err := run(t, "agent.approve", map[string]any{"id": r.ID, "ttl": "1h"})
	if err != nil {
		t.Fatal(err)
	}
	pairs := map[string]string{}
	for _, p := range v.(view.KeyValue).Pairs {
		pairs[p.Key] = p.Value
	}
	if want := host + " (xn--gthub-n2e.com) ends in a slash"; !strings.Contains(pairs["grant"], want) {
		t.Errorf("the answer says %q, want it to say %q", pairs["grant"], want)
	}
}
