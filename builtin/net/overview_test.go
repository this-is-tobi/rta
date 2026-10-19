package net

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/view"
)

// calm is a machine with a route, six resolvers, a handful of hosts-file lines
// of its owner's and one interface: the fixture each test bends one reading of.
func calm() netFacts {
	return netFacts{
		route:   routeFact{found: true, route: defaultRoute{gateway: "192.168.1.254", iface: "en0"}},
		dns:     dnsFact{servers: []string{"192.168.1.99", "9.9.9.9", "9.9.9.10", "192.168.1.254", "8.8.8.8", "fd0f:ee:b0::1"}},
		hosts:   hostsFact{custom: 5, parked: 1},
		traffic: trafficFact{rx: 35 * 1024, tx: 31437},
		addrs:   addrFact{v4: []string{"192.168.1.79"}},
	}
}

func rowsOf(f netFacts) ([]string, map[string]string) {
	kv := f.page()
	keys := make([]string, 0, len(kv.Pairs))
	values := map[string]string{}
	for _, p := range kv.Pairs {
		keys = append(keys, p.Key)
		values[p.Key] = p.Value
	}
	return keys, values
}

// The tile is the first eight lines of this answer, and what pages an on-call
// engineer is whether there is a way out and a resolver to ask, so those come
// first; the addresses that say which machine this is come last.
func TestTheCompactOverviewLeadsWithTheVerdictThenTheWayOut(t *testing.T) {
	keys, v := rowsOf(calm())
	want := []string{"status", "route", "traffic", "dns", "hosts", "ip"}
	if !slices.Equal(keys, want) {
		t.Fatalf("rows = %v, want %v", keys, want)
	}
	for key, value := range map[string]string{
		"status":  "ok",
		"route":   "via 192.168.1.254 on en0",
		"traffic": "rx 35.0 · tx 30.7 KiB/s",
		"dns":     "192.168.1.99 (+5 fallback)",
		"hosts":   "5 custom, 1 parked",
		"ip":      "192.168.1.79",
	} {
		if v[key] != value {
			t.Errorf("%s = %q, want %q", key, v[key], value)
		}
	}
}

// A proxy is a row only where there is one: "proxy none" says nothing on a tile
// and is a line of the eight that something else could have had.
func TestAProxyIsARowOnlyWhenOneIsSet(t *testing.T) {
	f := calm()
	f.proxy = "https_proxy=http://***@proxy.corp:3128"
	keys, v := rowsOf(f)
	if keys[len(keys)-1] != "proxy" || v["proxy"] != f.proxy {
		t.Errorf("rows = %v, proxy = %q", keys, v["proxy"])
	}
	if len(keys) > 8 {
		t.Errorf("%d rows, want at most eight", len(keys))
	}
}

func TestTheStatusRowNamesWhatIsMissing(t *testing.T) {
	with := func(change func(*netFacts)) netFacts {
		f := calm()
		change(&f)
		return f
	}
	for _, tc := range []struct {
		name  string
		facts netFacts
		want  string
	}{
		{"no default route", with(func(f *netFacts) { f.route = routeFact{} }), "fail — no default route"},
		{"no resolver", with(func(f *netFacts) { f.dns = dnsFact{} }), "fail — no resolver"},
		{"neither", with(func(f *netFacts) { f.route, f.dns = routeFact{}, dnsFact{} }), "fail — no default route, no resolver"},
		{"route unreadable", with(func(f *netFacts) { f.route = routeFact{err: errors.New("denied")} }), "warn — route could not be read"},
		{"dns unreadable", with(func(f *netFacts) { f.dns = dnsFact{err: errors.New("denied")} }), "warn — dns could not be read"},
		{"hosts unreadable", with(func(f *netFacts) { f.hosts = hostsFact{err: errors.New("denied")} }), "warn — hosts could not be read"},
		{"traffic unreadable", with(func(f *netFacts) { f.traffic = trafficFact{err: errors.New("denied")} }), "warn — traffic could not be read"},
		{"addresses unreadable", with(func(f *netFacts) { f.addrs = addrFact{err: errors.New("denied")} }), "warn — ip could not be read"},
		{"a failure outranks an unreadable row", with(func(f *netFacts) {
			f.dns, f.hosts = dnsFact{}, hostsFact{err: errors.New("denied")}
		}), "fail — no resolver"},
	} {
		if _, v := rowsOf(tc.facts); v["status"] != tc.want {
			t.Errorf("%s: status = %q, want %q", tc.name, v["status"], tc.want)
		}
	}
}

// A missing way out or a missing resolver is drawn as a failure in its own row
// too, and an unreadable one as the amber that says "not known".
func TestTheRowsThatFailAreDrawnWithAWordTheThemeGrades(t *testing.T) {
	f := calm()
	f.route, f.dns = routeFact{}, dnsFact{}
	_, v := rowsOf(f)
	for _, key := range []string{"status", "route", "dns"} {
		if got := theme.ClassifyStatus(v[key]); got != theme.StatusBad {
			t.Errorf("%s = %q draws as %v, want red", key, v[key], got)
		}
	}

	f = calm()
	f.route = routeFact{err: errors.New("denied")}
	f.dns = dnsFact{err: errors.New("denied")}
	f.hosts = hostsFact{err: errors.New("denied")}
	f.traffic = trafficFact{err: errors.New("denied")}
	f.addrs = addrFact{err: errors.New("denied")}
	keys, v := rowsOf(f)
	if len(keys) != 6 {
		t.Errorf("rows = %v: an unreadable row must stay a row", keys)
	}
	for _, key := range []string{"status", "route", "traffic", "dns", "hosts", "ip"} {
		if got := theme.ClassifyStatus(v[key]); got != theme.StatusWarn {
			t.Errorf("%s = %q draws as %v, want amber", key, v[key], got)
		}
	}
	for _, key := range []string{"route", "traffic", "dns", "hosts", "ip"} {
		if !strings.HasPrefix(v[key], "could not be read — denied") {
			t.Errorf("%s = %q, want it to say it could not be read and why", key, v[key])
		}
	}

	_, v = rowsOf(calm())
	if got := theme.ClassifyStatus(v["status"]); got != theme.StatusGood {
		t.Errorf("status = %q draws as %v, want green", v["status"], got)
	}
}

func TestDNSNamesTheFirstResolverAndCountsTheRest(t *testing.T) {
	for _, tc := range []struct {
		servers []string
		want    string
	}{
		{[]string{"1.1.1.1"}, "1.1.1.1"},
		{[]string{"1.1.1.1", "9.9.9.9"}, "1.1.1.1 (+1 fallback)"},
		{[]string{"1.1.1.1", "9.9.9.9", "8.8.8.8"}, "1.1.1.1 (+2 fallback)"},
	} {
		f := calm()
		f.dns = dnsFact{servers: tc.servers}
		if _, v := rowsOf(f); v["dns"] != tc.want {
			t.Errorf("dns of %v = %q, want %q", tc.servers, v["dns"], tc.want)
		}
	}
	f := calm()
	f.dns = dnsFact{}
	if _, v := rowsOf(f); v["dns"] != "fail — none configured" {
		t.Errorf("dns of nothing = %q", v["dns"])
	}
}

func TestTheRouteWithoutAGatewayNamesOnlyItsInterface(t *testing.T) {
	f := calm()
	f.route = routeFact{found: true, route: defaultRoute{iface: "wg0"}}
	if _, v := rowsOf(f); v["route"] != "on wg0" {
		t.Errorf("route = %q, want a point-to-point route said as it is", v["route"])
	}
	f.route = routeFact{}
	if _, v := rowsOf(f); v["route"] != "fail — no default route" {
		t.Errorf("route = %q", v["route"])
	}
}

// Two addresses fit a tile and the rest are counted; an IPv6 host with no IPv4
// address at all says so with its IPv6, not with nothing.
func TestTheAddressRowIsIPv4AndCapped(t *testing.T) {
	f := calm()
	f.addrs = addrFact{v4: []string{"192.168.1.79", "10.8.0.2", "172.16.0.9", "10.1.1.1"}}
	if _, v := rowsOf(f); v["ip"] != "192.168.1.79 · 10.8.0.2 (+2)" {
		t.Errorf("ip = %q", v["ip"])
	}
	f.addrs = addrFact{v6: []string{"2001:db8::79", "2001:db8::80"}}
	if _, v := rowsOf(f); v["ip"] != "2001:db8::79" {
		t.Errorf("ip of a host with only IPv6 = %q", v["ip"])
	}
	f.addrs = addrFact{}
	if _, v := rowsOf(f); v["ip"] != "none active" {
		t.Errorf("ip of nothing = %q", v["ip"])
	}
}

// What the owner put in the hosts file is counted by name, as net.hosts.list
// lists it, and the file's stock lines are not the owner's.
func TestTheHostsRowCountsTheOwnersNamesAndTheParkedOnes(t *testing.T) {
	lines := []string{
		"##",
		"# Host Database",
		"127.0.0.1\tlocalhost",
		"255.255.255.255\tbroadcasthost",
		"::1             localhost",
		"::1 ip6-localhost ip6-loopback",
		"fe00::0 ip6-localnet",
		"ff02::1 ip6-allnodes",
		"127.0.1.1 build-1.corp build-1",
		"10.0.0.5 api.corp db.corp   # staging",
		"10.0.0.6 cache.corp",
		"# 10.0.0.7 old.corp",
		"#10.0.0.8 older.corp legacy.corp",
		"# just a note about the file",
	}
	custom, parked := hostsCounts(lines, "build-1.corp")
	if custom != 3 || parked != 3 {
		t.Errorf("custom, parked = %d, %d; want 3, 3", custom, parked)
	}
	custom, parked = hostsCounts(nil, "build-1")
	if custom != 0 || parked != 0 {
		t.Errorf("an empty file counted %d, %d", custom, parked)
	}

	f := calm()
	f.hosts = hostsFact{custom: 0}
	if _, v := rowsOf(f); v["hosts"] != "0 custom" {
		t.Errorf("hosts = %q", v["hosts"])
	}
	f.hosts = hostsFact{custom: 2}
	if _, v := rowsOf(f); v["hosts"] != "2 custom" {
		t.Errorf("hosts = %q", v["hosts"])
	}
}

// One line per row at 80 columns, and no more than eight: a wrapped row is two
// lines of a tile that is cut at eight.
func TestTheCompactOverviewIsOneLinePerRowAtEightyColumns(t *testing.T) {
	proxied := calm()
	proxied.proxy = "https_proxy=http://***@proxy.corp:3128"
	proxied.addrs = addrFact{v4: []string{"192.168.1.79", "10.8.0.2", "172.16.0.9"}}
	broken := calm()
	broken.route, broken.dns = routeFact{}, dnsFact{}
	for name, f := range map[string]netFacts{"calm": calm(), "proxied": proxied, "broken": broken} {
		kv := f.page()
		var out bytes.Buffer
		if err := cli.Render(&out, kv, cli.Options{Format: cli.Pretty, NoColor: true, Width: 80}); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
		if len(lines) != len(kv.Pairs) || len(lines) > 8 {
			t.Errorf("%s: %d pairs drew %d lines, want one each and at most 8:\n%s", name, len(kv.Pairs), len(lines), out.String())
		}
	}
}

func TestTheOverviewCarriesItsFiguresAsNumbers(t *testing.T) {
	kv := calm().page()
	for name, want := range map[string]float64{
		"default_route":       1,
		"nameservers":         6,
		"hosts_custom":        5,
		"hosts_parked":        1,
		"rx_bytes_per_second": 35 * 1024,
		"tx_bytes_per_second": 31437,
	} {
		if got, there := kv.Numbers[name]; !there || math.Abs(got-want) > 1e-9 {
			t.Errorf("numbers[%s] = %v (present: %v), want %v", name, got, there, want)
		}
	}

	f := calm()
	f.route = routeFact{}
	if kv := f.page(); kv.Numbers["default_route"] != 0 {
		t.Errorf("default_route = %v for a host with none", kv.Numbers["default_route"])
	}

	f = calm()
	f.route = routeFact{err: errors.New("denied")}
	f.dns = dnsFact{err: errors.New("denied")}
	f.hosts = hostsFact{err: errors.New("denied")}
	f.traffic = trafficFact{err: errors.New("denied")}
	kv = f.page()
	for _, name := range []string{"default_route", "nameservers", "hosts_custom", "hosts_parked", "rx_bytes_per_second", "tx_bytes_per_second"} {
		if _, there := kv.Numbers[name]; there {
			t.Errorf("numbers[%s] is present for a reading the page does not show", name)
		}
	}
}

// The page that enter opens keeps every fact the tile used to show, under the
// verdict and the route: the interfaces with both address families, every
// resolver, the proxy whether or not one is set, and the throughput row that
// says what it leaves out.
func TestTheDetailSummaryKeepsTheOldRowsUnderTheVerdict(t *testing.T) {
	f := calm()
	kv := f.summary()
	keys := make([]string, len(kv.Pairs))
	for i, p := range kv.Pairs {
		keys[i] = p.Key
	}
	if len(keys) < 5 || keys[0] != "status" || keys[1] != "route" {
		t.Fatalf("summary rows = %v, want the status and the route first", keys)
	}
	for _, want := range []string{"dns", "proxy", throughputKey} {
		if !slices.Contains(keys, want) {
			t.Errorf("summary rows %v lack %q", keys, want)
		}
	}
	for _, p := range kv.Pairs {
		if p.Key == "dns" && p.Value != strings.Join(f.dns.servers, " · ") {
			t.Errorf("summary dns = %q, want every resolver of the fixture: %q", p.Value, strings.Join(f.dns.servers, " · "))
		}
	}
}

func TestTheLiveOverviewHasTheVerdictFirst(t *testing.T) {
	v, err := runOverview(t.Context(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	kv := v.(view.KeyValue)
	if len(kv.Pairs) < 5 || kv.Pairs[0].Key != "status" || kv.Pairs[1].Key != "route" {
		t.Fatalf("pairs = %v, want the status and the route first", kv.Pairs)
	}
	if len(kv.Pairs) > 8 {
		t.Errorf("%d rows, want at most eight", len(kv.Pairs))
	}
	if _, there := kv.Numbers["default_route"]; !there {
		t.Errorf("no default_route in %v", kv.Numbers)
	}
}
