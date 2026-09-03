package operator

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// The scrypt default is the point in production and a tax here.
	ScryptWorkFactor = 10
	os.Exit(m.Run())
}

func freshHome(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

func TestInitUnlockRoundTrips(t *testing.T) {
	freshHome(t)
	s, verr := Init("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	if s.Fingerprint() == "" || s.Fingerprint() != Fingerprint() {
		t.Fatalf("signer fingerprint %q does not match the stored key's %q", s.Fingerprint(), Fingerprint())
	}
	again, verr := Unlock("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	if again.Fingerprint() != s.Fingerprint() {
		t.Fatal("unlock produced a different key")
	}
}

func TestAWrongPassphraseIsNamedAsSuch(t *testing.T) {
	freshHome(t)
	if _, verr := Init("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	_, verr := Unlock("wrong horse")
	if verr == nil {
		t.Fatal("a wrong passphrase unlocked the key")
	}
	if verr.Code != "core.operator.passphrase" {
		t.Fatalf("code = %s, want core.operator.passphrase", verr.Code)
	}
}

func TestInitRefusesOverAnExistingKey(t *testing.T) {
	freshHome(t)
	if _, verr := Init("one"); verr != nil {
		t.Fatal(verr)
	}
	_, verr := Init("two")
	if verr == nil {
		t.Fatal("a second init overwrote the key")
	}
	if verr.Code != "core.operator.exists" {
		t.Fatalf("code = %s, want core.operator.exists", verr.Code)
	}
}

func writeRoster(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "operators")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The line `rta operator status` prints is the line a roster reads — the two
// ends of the enrollment flow must agree on the format or the flow is a doc
// that lies.
func TestTheRosterLineEnrolls(t *testing.T) {
	freshHome(t)
	s, verr := Init("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	line, verr := RosterLine("tobi")
	if verr != nil {
		t.Fatal(verr)
	}
	roster, _, err := LoadRoster(writeRoster(t, "# the ops team\n"+line+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	env := s.Sign("https://a.example", "nonce-1", VerbStatus, nil)
	id, ok := roster.Verify(env, "https://a.example")
	if !ok || id.Label != "tobi" {
		t.Fatalf("Verify = %q, %v — want tobi, true", id.Label, ok)
	}
	if !id.Expires.IsZero() {
		t.Fatalf("a bare roster line enrolled with an expiry: %v", id.Expires)
	}
}

// The relay: a hostile server the operator also talks to presents a victim
// server's challenge as its own and forwards the signed envelope. The
// signature covers the URL the operator aimed at, the victim verifies
// against its own — the relayed envelope names the wrong server.
func TestAnEnvelopeSignedForOneServerVerifiesOnNoOther(t *testing.T) {
	freshHome(t)
	s, verr := Init("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	line, _ := RosterLine("tobi")
	roster, _, err := LoadRoster(writeRoster(t, line+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	relayed := s.Sign("https://hostile.example", "victim-nonce", VerbGrantList, nil)
	if _, ok := roster.Verify(relayed, "https://victim.example"); ok {
		t.Fatal("an envelope aimed at one server verified on another")
	}
	if _, ok := roster.Verify(relayed, "https://hostile.example"); !ok {
		t.Fatal("the same envelope does not verify even where it was aimed — the binding is broken, not working")
	}
}

func TestVerifyRefusesWhatTheRosterNeverEnrolled(t *testing.T) {
	freshHome(t)
	s, verr := Init("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	line, _ := RosterLine("tobi")
	roster, _, err := LoadRoster(writeRoster(t, line+"\n"))
	if err != nil {
		t.Fatal(err)
	}

	const at = "https://a.example"
	env := s.Sign(at, "nonce-1", VerbGrantList, []byte(`{"a":1}`))
	if _, ok := roster.Verify(env, at); !ok {
		t.Fatal("the enrolled key was refused")
	}

	tampered := env
	tampered.Payload = []byte(`{"a":2}`)
	if _, ok := roster.Verify(tampered, at); ok {
		t.Fatal("a tampered payload verified")
	}
	replayedVerb := env
	replayedVerb.Verb = VerbStatus
	if _, ok := roster.Verify(replayedVerb, at); ok {
		t.Fatal("a signature travelled to a different verb")
	}
	otherNonce := env
	otherNonce.Nonce = "nonce-2"
	if _, ok := roster.Verify(otherNonce, at); ok {
		t.Fatal("a signature travelled to a different nonce")
	}

	// A different keypair, never enrolled, claiming the enrolled fingerprint.
	if err := os.Remove(Path()); err != nil {
		t.Fatal(err)
	}
	stranger, verr := Init("other pass")
	if verr != nil {
		t.Fatal(verr)
	}
	forged := stranger.Sign(at, "nonce-1", VerbGrantList, []byte(`{"a":1}`))
	forged.Fingerprint = env.Fingerprint
	if _, ok := roster.Verify(forged, at); ok {
		t.Fatal("an un-enrolled key verified")
	}
}

func TestTheRosterRefusesWeakPermissions(t *testing.T) {
	path := writeRoster(t, "tobi AAAA\n")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadRoster(path); err == nil {
		t.Fatal("a world-readable roster loaded")
	}
}

func TestTheRosterNamesAGarbledKey(t *testing.T) {
	if _, _, err := LoadRoster(writeRoster(t, "tobi not-a-key\n")); err == nil {
		t.Fatal("a garbled key loaded")
	}
	if _, _, err := LoadRoster(writeRoster(t, "# nobody\n")); err == nil {
		t.Fatal("an empty roster loaded")
	}
}

func TestOneKeyEnrollsOnce(t *testing.T) {
	freshHome(t)
	if _, verr := Init("p"); verr != nil {
		t.Fatal(verr)
	}
	line, _ := RosterLine("tobi")
	other, _ := RosterLine("also-tobi")
	if _, _, err := LoadRoster(writeRoster(t, line+"\n"+other+"\n")); err == nil {
		t.Fatal("the same key enrolled under two labels")
	}
}

func TestANonceSpendsExactlyOnce(t *testing.T) {
	n := NewNonces(0)
	nonce, err := n.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if !n.Consume(nonce) {
		t.Fatal("a fresh nonce did not spend")
	}
	if n.Consume(nonce) {
		t.Fatal("a nonce spent twice")
	}
	if n.Consume("never-issued") {
		t.Fatal("an invented nonce spent")
	}
}

func TestAnExpiredNonceDoesNotSpend(t *testing.T) {
	n := NewNonces(time.Nanosecond)
	nonce, err := n.Issue()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if n.Consume(nonce) {
		t.Fatal("an expired nonce spent")
	}
}

// A redirect would re-POST a signed envelope wherever a hostile server
// points, or move the response off TLS — there are two fixed endpoints and
// no legitimate hop, so the client refuses rather than follows.
func TestTheClientRefusesRedirects(t *testing.T) {
	freshHome(t)
	s, verr := Init("p")
	if verr != nil {
		t.Fatal(verr)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/operator/v1/challenge", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	verr = Client{URL: srv.URL, Signer: s}.Call(VerbStatus, nil, nil)
	if verr == nil {
		t.Fatal("a redirecting server was followed")
	}
	if !strings.Contains(verr.Message, "redirect") {
		t.Fatalf("the refusal does not name the redirect: %s", verr.Message)
	}
}

// Consume leaves its stale entry in order for GC; an issue-and-consume flood
// with one live head parked in front must not grow the slice without bound
// for a TTL window.
func TestAFloodCannotGrowTheNonceStoreUnbounded(t *testing.T) {
	n := NewNonces(time.Hour)
	parked, err := n.Issue()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2*nonceCap; i++ {
		nonce, err := n.Issue()
		if err != nil {
			t.Fatal(err)
		}
		n.Consume(nonce)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.order) > n.cap {
		t.Fatalf("order holds %d entries, cap is %d", len(n.order), n.cap)
	}
	_ = parked
}

// A role annotation subtracts from an enrollment; anything unrecognized in
// that position refuses the whole load, because a typo that silently meant
// "full operator" is the one failure a restriction must not have.
func TestARosterRoleParsesAndAnythingElseRefuses(t *testing.T) {
	freshHome(t)
	s, verr := Init("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	line, verr := RosterLine("dash")
	if verr != nil {
		t.Fatal(verr)
	}

	roster, _, err := LoadRoster(writeRoster(t, line+" role=read\n"))
	if err != nil {
		t.Fatal(err)
	}
	env := s.Sign("https://a.example", "nonce-1", VerbStatus, nil)
	id, ok := roster.Verify(env, "https://a.example")
	if !ok || id.Label != "dash" || id.Role != RoleRead {
		t.Fatalf("Verify = %q, %q, %v — want dash, read, true", id.Label, id.Role, ok)
	}

	explicit, _, err := LoadRoster(writeRoster(t, line+" role=full\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := explicit.Verify(env, "https://a.example"); !ok || got.Role != RoleFull {
		t.Fatalf("an explicit role=full parsed as %q, %v", got.Role, ok)
	}

	for _, bad := range []string{" roel=read", " role=admin", " read", " role=read role=full"} {
		if _, _, err := LoadRoster(writeRoster(t, line+bad+"\n")); err == nil {
			t.Fatalf("a roster line with %q loaded", bad)
		}
	}
}

// The expires annotation is subtract-only like role, and shares the
// grammar's failure direction: a date rta cannot read refuses the whole
// load, because a typo that silently meant "never expires" would hand a
// departed operator's key exactly the lifetime the annotation was written
// to end.
func TestARosterExpiryParsesAndRefusesAtItsDay(t *testing.T) {
	freshHome(t)
	s, verr := Init("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	line, verr := RosterLine("tobi")
	if verr != nil {
		t.Fatal(verr)
	}

	roster, _, err := LoadRoster(writeRoster(t, line+" role=read expires=2030-06-15\n"))
	if err != nil {
		t.Fatal(err)
	}
	env := s.Sign("https://a.example", "nonce-1", VerbStatus, nil)
	id, ok := roster.Verify(env, "https://a.example")
	if !ok || id.Role != RoleRead || id.Expires.IsZero() {
		t.Fatalf("Verify = %+v, %v — want a read row carrying its expiry", id, ok)
	}
	// The named day itself is already refused: "expires 2030-06-15" reads
	// as "gone by the 15th", anchored at that day's local midnight.
	day := time.Date(2030, 6, 15, 0, 0, 0, 0, time.Local)
	if id.Expired(day.Add(-time.Minute)) {
		t.Fatal("expired a minute before its day")
	}
	if !id.Expired(day) {
		t.Fatal("still enrolled on the day the row named")
	}

	// A date already past still loads — it is configuration that aged, not
	// a typo, and the row refuses per call all by itself; refusing the file
	// would cut every other operator off at the next restart.
	aged, _, err := LoadRoster(writeRoster(t, line+" expires=2020-01-01\n"))
	if err != nil {
		t.Fatalf("an aged expiry refused the whole roster: %v", err)
	}
	if got, ok := aged.Verify(env, "https://a.example"); !ok || !got.Expired(time.Now()) {
		t.Fatalf("the aged row should verify and read as expired: %+v, %v", got, ok)
	}

	for _, bad := range []string{
		" expires=tomorrow", " expires=2030-6-15", " expires=2030-06-15T10:00:00Z",
		" expires=2030-06-15 expires=2031-01-01",
	} {
		if _, _, err := LoadRoster(writeRoster(t, line+bad+"\n")); err == nil {
			t.Fatalf("a roster line with %q loaded", bad)
		}
	}
}

// Entries feeds `rta grant guard remote`, and a guard entry is
// grant-signing trust with no verb dispatch in between — so a read-only
// key must never appear in it, however the roster mixes its rows.
func TestReadOnlyKeysStayOutOfTheGuardEntries(t *testing.T) {
	freshHome(t)
	if _, verr := Init("one"); verr != nil {
		t.Fatal(verr)
	}
	full, _ := RosterLine("tobi")
	if err := os.Remove(Path()); err != nil {
		t.Fatal(err)
	}
	if _, verr := Init("two"); verr != nil {
		t.Fatal(verr)
	}
	readOnly, _ := RosterLine("dash")

	roster, _, err := LoadRoster(writeRoster(t, full+"\n"+readOnly+" role=read\n"))
	if err != nil {
		t.Fatal(err)
	}
	entries := roster.Entries()
	if len(entries) != 1 || entries[0].Label != "tobi" {
		t.Fatalf("Entries = %+v — want tobi alone", entries)
	}
	ops := roster.Operators()
	if len(ops) != 2 || ops[0] != (OperatorInfo{Label: "dash", Role: RoleRead}) ||
		ops[1] != (OperatorInfo{Label: "tobi", Role: RoleFull}) {
		t.Fatalf("Operators = %+v", ops)
	}
}

// A guard entry is grant-signing trust and the guard file is static once
// written, so provisioning is the one chance to keep an already-departed
// key out of it. A row that expires later stays in until `grant guard
// remote` is re-run — expiry gates the channel live, the guard only at
// provisioning, like every other roster edit.
func TestExpiredKeysStayOutOfTheGuardEntries(t *testing.T) {
	freshHome(t)
	if _, verr := Init("one"); verr != nil {
		t.Fatal(verr)
	}
	current, _ := RosterLine("tobi")
	if err := os.Remove(Path()); err != nil {
		t.Fatal(err)
	}
	if _, verr := Init("two"); verr != nil {
		t.Fatal(verr)
	}
	departed, _ := RosterLine("gone")

	roster, _, err := LoadRoster(writeRoster(t,
		current+" expires=2999-01-01\n"+departed+" expires=2020-01-01\n"))
	if err != nil {
		t.Fatal(err)
	}
	entries := roster.Entries()
	if len(entries) != 1 || entries[0].Label != "tobi" {
		t.Fatalf("Entries = %+v — want tobi alone", entries)
	}
	// Both still appear on the status page: the row that reads "expired"
	// is the one a person should go delete, and hiding it would hide the
	// chore.
	ops := roster.Operators()
	if len(ops) != 2 || ops[0].Expires != "2020-01-01" || ops[1].Expires != "2999-01-01" {
		t.Fatalf("Operators = %+v", ops)
	}
	if got := ops[0].String(); got != "gone (expired 2020-01-01)" {
		t.Fatalf("an expired row renders as %q", got)
	}
	if got := ops[1].String(); got != "tobi (expires 2999-01-01)" {
		t.Fatalf("a dated row renders as %q", got)
	}
	if got := (OperatorInfo{Label: "dash", Role: RoleRead, Expires: "2999-01-01"}).String(); got != "dash (read, expires 2999-01-01)" {
		t.Fatalf("a read, dated row renders as %q", got)
	}
}

// The read allowlist is closed: a verb this build never heard of — or one
// added without classifying it — is refused for read-only keys, and the
// zero Role, which only a bug could produce, allows nothing at all.
func TestARoleAllowsItsVerbsAndNothingElse(t *testing.T) {
	reads := []string{VerbStatus, VerbGrantList, VerbConsentList, VerbLockList}
	rest := []string{VerbGrantRevoke, VerbGrantPrepare, VerbGrantIssue, VerbConsentAnswer, VerbLockAdd, VerbLockRm, "grant.future"}
	for _, v := range append(append([]string{}, reads...), rest...) {
		if !RoleFull.Allows(v) {
			t.Fatalf("full refuses %s", v)
		}
		if Role("").Allows(v) {
			t.Fatalf("the zero role allows %s", v)
		}
	}
	for _, v := range reads {
		if !RoleRead.Allows(v) {
			t.Fatalf("read refuses %s", v)
		}
	}
	for _, v := range rest {
		if RoleRead.Allows(v) {
			t.Fatalf("read allows %s", v)
		}
	}
}

// Base64 is not injective by default: the char before a 32-byte key's
// padding carries two bits the lenient decoder ignores, so four spellings
// name one key. The review that caught this drew the full attack: the same
// key enrolled twice through an alias spelling — once role=read, once
// bare — would answer as a full operator and cross into the guard's
// signing set. One spelling per key, and one label per key by decoded
// bytes, must both hold.
func TestAnAliasSpellingOfAKeyDoesNotEnrollTwice(t *testing.T) {
	freshHome(t)
	if _, verr := Init("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	line, verr := RosterLine("dash")
	if verr != nil {
		t.Fatal(verr)
	}
	fields := strings.Fields(line)
	encoded := fields[1]
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	mal := []byte(encoded)
	last := len(mal) - 2 // the data char before "=", whose low 2 bits are padding
	mal[last] = alphabet[strings.IndexByte(alphabet, mal[last])^1]
	alias := string(mal)
	aliased, err := base64.StdEncoding.DecodeString(alias)
	canonical, _ := base64.StdEncoding.DecodeString(encoded)
	if alias == encoded || err != nil || !bytes.Equal(aliased, canonical) {
		t.Fatal("the alias does not leniently decode to the same key — the test is not testing")
	}

	if _, _, err := LoadRoster(writeRoster(t, "mallory "+alias+"\n")); err == nil {
		t.Fatal("a non-canonical key spelling enrolled")
	}
	both := line + " role=read\nmallory " + alias + "\n"
	if _, _, err := LoadRoster(writeRoster(t, both)); err == nil {
		t.Fatal("one key enrolled under two labels through an alias spelling")
	}
}
