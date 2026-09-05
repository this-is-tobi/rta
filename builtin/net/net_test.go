package net

import (
	"context"
	"errors"
	stdnet "net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func req(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, false)
}

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

// The file input splits by blast radius, not by capability family, and the
// split is the whole fix: on the four that write, an MCP caller choosing the
// path turned "allowlist net.hosts.add and grant one hostname" into "append a
// line to any file this process can write". On the two that read, it is the
// ordinary sysadmin job the flag exists for — looking into a container's or a
// chroot's hosts file — and stays open. Pinned per capability because a
// future writer added to this plugin has to make the same choice on purpose.
func TestOnlyTheWritingFileCapabilitiesHideTheirPathFromMCP(t *testing.T) {
	want := map[string]bool{
		"net.hosts.list":    false,
		"net.hosts.add":     true,
		"net.hosts.toggle":  true,
		"net.hosts.rm":      true,
		"net.resolver.list": false,
		"net.resolver.set":  true,
	}
	seen := map[string]bool{}
	for _, c := range Plugin().Capabilities {
		for _, f := range c.Inputs {
			if f.Name != "file" {
				continue
			}
			seen[c.ID] = true
			local, classified := want[c.ID]
			if !classified {
				t.Errorf("%s declares a file input nobody has classified", c.ID)
				continue
			}
			if f.Local != local {
				t.Errorf("%s file input Local = %v, want %v", c.ID, f.Local, local)
			}
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s no longer declares a file input", id)
		}
	}
}

// The host is the caller's choice on all three of these, and what comes back
// — a banner, an open/closed map, whatever a written payload provokes —
// arrives in an agent's context the same way http.get's response does. A
// future capability that dials a caller-chosen destination has to make the
// same choice on purpose, the same reason net.hosts's Local split is pinned
// above.
func TestHostReachingCapabilitiesNeedAGrantScopedToHost(t *testing.T) {
	want := map[string]bool{"net.probe": true, "net.send": true, "net.port": true}
	seen := map[string]bool{}
	for _, c := range Plugin().Capabilities {
		needsGrant, classified := want[c.ID]
		if !classified {
			continue
		}
		seen[c.ID] = true
		if c.NeedsGrant != needsGrant {
			t.Errorf("%s.NeedsGrant = %v, want %v", c.ID, c.NeedsGrant, needsGrant)
		}
		if c.Scope != "host" {
			t.Errorf("%s.Scope = %q, want %q", c.ID, c.Scope, "host")
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s no longer exists", id)
		}
	}
}

func TestParsePorts(t *testing.T) {
	tests := []struct {
		spec    string
		want    []int
		wantErr bool
	}{
		{"22,80,443", []int{22, 80, 443}, false},
		{"8000-8002", []int{8000, 8001, 8002}, false},
		{"80, 22 ,80", []int{22, 80}, false}, // dedup + sort + spaces
		{"1-3,2", []int{1, 2, 3}, false},
		{"abc", nil, true},
		{"10-5", nil, true},
		{"0", nil, true},
		{"70000", nil, true},
		// Seven bytes that used to buy 65,535 goroutines and ~180 MiB before
		// the first dial, from a grant-free MCP read.
		{"1-65535", nil, true},
		// And the shape a cap on the *result* would miss: each part is
		// individually legal and within the cap, and the dedup map hides the
		// repetition, so only counting before expanding bounds the CPU.
		{"1-4096,1-4096", nil, true},
	}
	for _, tt := range tests {
		got, err := parsePorts(tt.spec)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parsePorts(%q): want error", tt.spec)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePorts(%q): %v", tt.spec, err)
			continue
		}
		if len(got) != len(tt.want) {
			t.Errorf("parsePorts(%q) = %v, want %v", tt.spec, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parsePorts(%q) = %v, want %v", tt.spec, got, tt.want)
				break
			}
		}
	}
}

func TestPortScan(t *testing.T) {
	// Open a real listener; scan it plus a port that is almost surely closed.
	ln, err := stdnet.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	openPort := ln.Addr().(*stdnet.TCPAddr).Port

	v, err := runPort(context.Background(), req(map[string]any{
		"host":    "127.0.0.1",
		"ports":   strconv.Itoa(openPort) + ",1",
		"timeout": 2,
	}))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	states := map[string]string{}
	for _, row := range tbl.Rows {
		states[row[0]] = row[1]
	}
	if states[strconv.Itoa(openPort)] != "open" {
		t.Errorf("listener port reported %q", states[strconv.Itoa(openPort)])
	}
	if states["1"] != "closed" {
		t.Errorf("port 1 reported %q", states["1"])
	}
}

func TestPortBadSpec(t *testing.T) {
	_, err := runPort(context.Background(), req(map[string]any{"host": "x", "ports": "nope", "timeout": 1}))
	ve := view.AsError(err, "x")
	if ve.Code != "net.port.badspec" || ve.Hint == "" {
		t.Errorf("want coded badspec error with hint, got %+v", ve)
	}
}

func TestDNSBadType(t *testing.T) {
	_, err := runDNS(context.Background(), req(map[string]any{"name": "example.com", "type": "WAT"}))
	ve := view.AsError(err, "x")
	if ve.Code != "net.dns.badtype" || ve.Hint == "" {
		t.Errorf("want net.dns.badtype with hint, got %+v", ve)
	}
}

func TestDNSLocalhost(t *testing.T) {
	v, err := runDNS(context.Background(), req(map[string]any{"name": "localhost", "type": "A"}))
	if err != nil {
		t.Skipf("resolver unavailable: %v", err)
	}
	tbl := v.(view.Table)
	if len(tbl.Rows) == 0 {
		t.Error("localhost resolved to nothing")
	}
}

// autoTypes is what makes `net dns` answer the question people mean: an
// address gets a reverse lookup, a name gets its address records.
func TestDNSAutoPicksTypesFromTheQuery(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"example.com", []string{"A", "AAAA", "CNAME"}},
		{"1.1.1.1", []string{"PTR"}},
		{"2606:4700:4700::1111", []string{"PTR"}},
	} {
		if got := autoTypes(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("autoTypes(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestDNSAutoReverseResolvesLoopback(t *testing.T) {
	v, err := runDNS(context.Background(), req(map[string]any{"name": "127.0.0.1", "type": "auto", "timeout": 5}))
	if err != nil {
		t.Skipf("no reverse record for the loopback here: %v", err)
	}
	tbl := v.(view.Table)
	if len(tbl.Rows) == 0 || tbl.Rows[0][0] != "PTR" {
		t.Errorf("reverse lookup = %v", tbl.Rows)
	}
}

// A single-type miss is a reportable failure; the auto set tolerates misses
// because asking broadly means most questions come back empty.
func TestDNSNoRecordsIsCoded(t *testing.T) {
	_, err := runDNS(context.Background(), req(map[string]any{
		"name": "no-such-host.invalid", "type": "A", "timeout": 5,
	}))
	ve := view.AsError(err, "x")
	if ve.Code != "net.dns.failed" && ve.Code != "net.dns.norecords" {
		t.Errorf("want a coded lookup failure, got %+v", ve)
	}
	if ve.Hint == "" && ve.Code == "net.dns.norecords" {
		t.Error("a no-records answer should suggest what to try next")
	}
}

func TestDNSDetailReportsTheResolver(t *testing.T) {
	v, err := runDNS(context.Background(), req(map[string]any{
		"name": "localhost", "type": "A", "detail": true,
	}))
	if err != nil {
		t.Skipf("resolver unavailable: %v", err)
	}
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("--detail should compose a page, got %s", view.TypeOf(v))
	}
	kv, ok := s.Items[0].View.(view.KeyValue)
	if !ok {
		t.Fatalf("first section = %s", view.TypeOf(s.Items[0].View))
	}
	var resolver string
	for _, p := range kv.Pairs {
		if p.Key == "resolver" {
			resolver = p.Value
		}
	}
	if resolver != "system" {
		t.Errorf("resolver = %q, want the system resolver by default", resolver)
	}
}

// resolverFor names the resolver being queried, and defaults a bare address
// to port 53 — the flag takes "1.1.1.1", not "1.1.1.1:53".
func TestResolverForLabelsAndDefaultsPort(t *testing.T) {
	if _, label := resolverFor(""); label != "system" {
		t.Errorf("no server = %q, want system", label)
	}
	if _, label := resolverFor("1.1.1.1"); label != "1.1.1.1:53" {
		t.Errorf("bare address = %q", label)
	}
	if _, label := resolverFor("9.9.9.9:5353"); label != "9.9.9.9:5353" {
		t.Errorf("explicit port = %q", label)
	}
}

func TestPingLocalhost(t *testing.T) {
	v, err := runPing(context.Background(), req(map[string]any{"host": "127.0.0.1", "count": 1, "timeout": 3}))
	if err != nil {
		// Unprivileged ICMP is environment-dependent; skip rather than flake.
		t.Skipf("ping unavailable in this environment: %v", err)
	}
	kv := v.(view.KeyValue)
	found := false
	for _, p := range kv.Pairs {
		if p.Key == "avg" && p.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("ping stats missing avg")
	}
}

func TestMaskProxy(t *testing.T) {
	tests := []struct{ in, want string }{
		{"http://user:secret@proxy.corp:3128", "http://***@proxy.corp:3128"},
		{"http://proxy.corp:3128", "http://proxy.corp:3128"},
		{"not a url at all", "not a url at all"},
	}
	for _, tt := range tests {
		got := maskProxy(tt.in)
		if got != tt.want {
			t.Errorf("maskProxy(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if strings.Contains(got, "secret") {
			t.Errorf("maskProxy(%q) leaked the credential: %q", tt.in, got)
		}
	}
}

func TestProxySummaryMasksCredentials(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://admin:hunter2@proxy.corp:3128")
	t.Setenv("https_proxy", "")
	t.Setenv("http_proxy", "")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("all_proxy", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Setenv("NO_PROXY", "")
	got := proxySummary()
	if strings.Contains(got, "hunter2") || strings.Contains(got, "admin") {
		t.Fatalf("proxy summary leaked credentials: %q", got)
	}
	if !strings.Contains(got, "proxy.corp:3128") {
		t.Errorf("proxy host missing: %q", got)
	}
}

func TestDNSServersParsesResolvConf(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "resolv.conf")
	content := "# comment\ndomain local\nnameserver 192.168.1.1\nnameserver 9.9.9.9\n"
	if err := os.WriteFile(fixture, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := resolvConf
	resolvConf = fixture
	defer func() { resolvConf = orig }()

	if got := dnsServers(); got != "192.168.1.1 · 9.9.9.9" {
		t.Errorf("dnsServers() = %q", got)
	}
}

// TestInfoIsWellFormed runs against the real host: shape only, values are
// machine-dependent.
func TestInfoIsWellFormed(t *testing.T) {
	v, err := runInfo(context.Background(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	kv := v.(view.KeyValue)
	keys := map[string]bool{}
	for _, p := range kv.Pairs {
		if p.Value == "" {
			t.Errorf("empty value for %q", p.Key)
		}
		keys[p.Key] = true
	}
	for _, want := range []string{"dns", "proxy", "throughput"} {
		if !keys[want] {
			t.Errorf("missing %q pair", want)
		}
	}
}

// The cap is a bound, not a blanket refusal: exactly maxScanPorts is fine, and
// one more is not. Without both halves, "refuse everything" would pass.
func TestTheScanCapIsABoundAndNotARefusal(t *testing.T) {
	at := strconv.Itoa(maxScanPorts)
	got, err := parsePorts("1-" + at)
	if err != nil {
		t.Fatalf("parsePorts(1-%s) = %v, want the cap to be inclusive", at, err)
	}
	if len(got) != maxScanPorts {
		t.Errorf("got %d ports, want %d", len(got), maxScanPorts)
	}
	if _, err := parsePorts("1-" + strconv.Itoa(maxScanPorts+1)); !errors.Is(err, errTooManyPorts) {
		t.Errorf("one past the cap = %v, want errTooManyPorts", err)
	}
}

// **The fan-out is the port count, not the concurrency bound.**
//
// The old loop spawned one goroutine per port and acquired the semaphore
// *inside* it, so the bound applied to the dials and every port got a live
// goroutine the instant the loop ran. The cap alone would not catch a
// reintroduction of that shape — it would just make the spike smaller — so
// this measures the fan-out directly.
//
// Against a closed loopback port, so nothing leaves the machine and every dial
// fails immediately.
func TestThePortScanDoesNotSpawnAGoroutinePerPort(t *testing.T) {
	base := runtime.NumGoroutine()
	peak := make(chan int, 1)
	done := make(chan struct{})
	go func() {
		high := 0
		for {
			select {
			case <-done:
				peak <- high
				return
			default:
			}
			if n := runtime.NumGoroutine(); n > high {
				high = n
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// 2000 distinct ports, all closed. Enough that a goroutine-per-port shape
	// is unmistakable against a 1ms sampler.
	r := req(map[string]any{"host": "127.0.0.1", "ports": "1-2000", "timeout": 1})
	if _, err := runPort(t.Context(), r); err != nil {
		t.Fatalf("scan: %v", err)
	}
	close(done)

	// 2000 ports through a 64-worker pool: the pool's own goroutines plus the
	// test's, nowhere near one per port. The old shape peaked at the port
	// count.
	if got := <-peak - base; got > portScanWorkers*2 {
		t.Errorf("peak was %d goroutines above baseline for 2000 ports — the bound is on "+
			"the dials and the fan-out is the port count", got)
	}
}
