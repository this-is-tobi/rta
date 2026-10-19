package net

import (
	"context"
	"errors"
	"fmt"
	stdnet "net"
	"os"
	"slices"
	"strings"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"

	"github.com/this-is-tobi/rta/builtin/internal/pathin"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// grade is how a row stands: a plain fact, or a failure or an unknown that
// the status row has to name.
type grade int

const (
	ungraded grade = iota
	gradeWarn
	gradeFail
)

// netFacts is what the compact overview says, read once and kept apart from the
// words that say it, so a test can put a host with no default route on the table
// without having to be one.
type netFacts struct {
	route   routeFact
	dns     dnsFact
	hosts   hostsFact
	traffic trafficFact
	addrs   addrFact
	// proxy is the masked proxy environment, empty where there is none.
	proxy string
}

// Each reading carries its own error: a page whose hosts file could not be read
// still has a route, and says in the hosts row why it has no count.
type routeFact struct {
	route defaultRoute
	found bool
	err   error
}

type dnsFact struct {
	servers []string
	err     error
}

type hostsFact struct {
	custom, parked int
	err            error
}

type trafficFact struct {
	rx, tx uint64
	err    error
}

type addrFact struct {
	v4, v6 []string
	err    error
}

// row is one line of the page and what it adds to the verdict.
type row struct {
	key, value string
	grade      grade
	// headline is the fact in a few words, for the status row.
	headline string
}

// readNetFacts reads everything the compact overview says. The route table and
// the hosts file are two reads of local state that cost nothing a refresh would
// notice; the 500 ms throughput window is the cost this page has always had, and
// the interfaces are listed once for the addresses and the throughput both.
func readNetFacts(ctx context.Context, req plugin.Request) netFacts {
	var f netFacts
	f.route.route, f.route.found, f.route.err = readDefaultRoute()
	f.dns = readDNS()
	f.hosts = readHostsFact(req)
	f.addrs, f.traffic = readInterfaces(ctx, f.route.route.iface)
	f.proxy = proxySummary()
	if f.proxy == "none" {
		f.proxy = ""
	}
	return f
}

func readDNS() dnsFact {
	data, err := pathin.ReadFile(resolvConf, maxResolvBytes)
	if err != nil {
		return dnsFact{err: err}
	}
	var servers []string
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 2 && f[0] == "nameserver" {
			servers = append(servers, f[1])
		}
	}
	return dnsFact{servers: servers}
}

// text is the resolvers as the detailed page lists them, all of them.
func (d dnsFact) text() string {
	switch {
	case d.err != nil:
		return "unknown"
	case len(d.servers) == 0:
		return "none configured"
	}
	return strings.Join(d.servers, " · ")
}

func readHostsFact(req plugin.Request) hostsFact {
	lines, verr := readLines(req, hostsFile, maxHostsBytes)
	if verr != nil {
		return hostsFact{err: verr}
	}
	own, _ := os.Hostname()
	custom, parked := hostsCounts(lines, own)
	return hostsFact{custom: custom, parked: parked}
}

// stockHostNames are the names a hosts file ships with and nobody added: what an
// operating system puts there so that localhost resolves.
var stockHostNames = []string{
	"localhost", "broadcasthost", "ip6-localhost", "ip6-loopback", "ip6-localnet",
	"ip6-mcastprefix", "ip6-allnodes", "ip6-allrouters", "ip6-allhosts",
}

// hostsCounts is how many names the owner of the hosts file has put in it and
// how many of those are parked (switched off by a comment, which is how
// `net hosts toggle` keeps them), by name as `net hosts list` counts. rta marks
// nothing on the lines it adds, so "the lines rta changed" cannot be told from
// the owner's own; what can be told from the stock ones is these. The stock
// names are the operating system's, and the machine's own, which Debian maps to
// 127.0.1.1.
func hostsCounts(lines []string, own string) (custom, parked int) {
	short, _, _ := strings.Cut(own, ".")
	stock := func(name string) bool {
		return slices.Contains(stockHostNames, name) || strings.EqualFold(name, own) || strings.EqualFold(name, short)
	}
	for _, e := range parseHosts(lines) {
		for _, name := range e.names {
			switch {
			case stock(name):
			case e.enabled:
				custom++
			default:
				parked++
			}
		}
	}
	return custom, parked
}

// readInterfaces lists the interfaces once and reads from them the addresses the
// page shows and the traffic it counts. via is the interface the default route
// leaves by: its addresses come first.
func readInterfaces(ctx context.Context, via string) (addrFact, trafficFact) {
	ifs, err := stdnet.Interfaces()
	if err != nil {
		return addrFact{err: err}, trafficFact{err: err}
	}
	return addressesOf(ifs, via), sampleTraffic(ctx, ifs)
}

// addressesOf collects the addresses of the interfaces that are up and are not
// the loopback or a container host's plumbing, those of the default route's
// first. A link-local address is not one anybody connects to.
func addressesOf(ifs []stdnet.Interface, via string) addrFact {
	var f addrFact
	ordered := slices.Clone(ifs)
	slices.SortStableFunc(ordered, func(a, b stdnet.Interface) int {
		switch {
		case a.Name == via && b.Name != via:
			return -1
		case b.Name == via && a.Name != via:
			return 1
		}
		return 0
	})
	for _, in := range ordered {
		if in.Flags&stdnet.FlagUp == 0 || in.Flags&stdnet.FlagLoopback != 0 || containerPlumbing(in.Name) {
			continue
		}
		addrs, _ := in.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*stdnet.IPNet)
			if !ok || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			if ip4 := ipn.IP.To4(); ip4 != nil {
				f.v4 = append(f.v4, ip4.String())
			} else {
				f.v6 = append(f.v6, ipn.IP.String())
			}
		}
	}
	return f
}

// sampleTraffic samples rx and tx over 500ms and reports per-second rates,
// summed over the interfaces that are traffic on the network.
func sampleTraffic(ctx context.Context, ifs []stdnet.Interface) trafficFact {
	const sample = 500 * time.Millisecond
	keep := countedIn(ifs)
	before, err := gnet.IOCountersWithContext(ctx, true)
	if err != nil || len(before) == 0 {
		return trafficFact{err: errors.Join(errors.New("no interface counters"), err)}
	}
	select {
	case <-ctx.Done():
		return trafficFact{err: ctx.Err()}
	case <-time.After(sample):
	}
	after, err := gnet.IOCountersWithContext(ctx, true)
	if err != nil || len(after) == 0 {
		return trafficFact{err: errors.Join(errors.New("no interface counters"), err)}
	}
	rx, tx := ratesOf(before, after, sample, keep)
	return trafficFact{rx: rx, tx: tx}
}

// text is the throughput as the detailed page words it.
func (t trafficFact) text() string {
	if t.err != nil {
		return "unreadable"
	}
	return fmt.Sprintf("rx %s/s · tx %s/s", format.Bytes(t.rx), format.Bytes(t.tx))
}

// rates is the throughput at a tile's width: the unit said once when both
// directions are in it.
func (t trafficFact) rates() string {
	rx, tx := format.Bytes(t.rx), format.Bytes(t.tx)
	if i := strings.LastIndexByte(rx, ' '); i > 0 && strings.HasSuffix(tx, rx[i:]) {
		return "rx " + rx[:i] + " · tx " + tx + "/s"
	}
	return "rx " + rx + "/s · tx " + tx + "/s"
}

// rows are the lines after the verdict, in the order they are drawn.
func (f netFacts) rows() []row {
	return []row{f.routeRow(), f.trafficRow(), f.dnsRow(), f.hostsRow(), f.addrRow()}
}

// page is the compact overview: the verdict, then the way out, then the traffic
// on it and the resolver that names things, then what the owner changed, and the
// addresses — which say which machine this is — last. A tile is cut from the
// bottom, so the order is the argument.
func (f netFacts) page() view.KeyValue {
	rows := f.rows()
	kv := view.KeyValue{Numbers: f.numbers(), Pairs: []view.Pair{statusOf(rows)}}
	for _, r := range rows {
		kv.Pairs = append(kv.Pairs, view.Pair{Key: r.key, Value: r.value})
	}
	if f.proxy != "" {
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "proxy", Value: f.proxy})
	}
	return kv
}

// summary is the detailed page's first section: the verdict and the route, then
// every fact the tile used to show — all the interfaces with both address
// families, every resolver, the proxy whether or not one is set, and the
// throughput under the key that says what it leaves out.
func (f netFacts) summary() view.KeyValue {
	rows := f.rows()
	kv := view.KeyValue{Numbers: f.numbers(), Pairs: []view.Pair{statusOf(rows), {Key: rows[0].key, Value: rows[0].value}}}
	kv.Pairs = append(kv.Pairs, ifacePairs()...)
	proxy := f.proxy
	if proxy == "" {
		proxy = "none"
	}
	kv.Pairs = append(kv.Pairs,
		view.Pair{Key: "dns", Value: f.dns.text()},
		view.Pair{Key: "proxy", Value: proxy},
		view.Pair{Key: throughputKey, Value: f.traffic.text()},
	)
	return kv
}

// statusOf is the page's first line: "ok", or what is wrong in a few words. A
// failure is named before anything that merely could not be read, and all of the
// failures are named, because "no default route" and "no resolver" are two
// things to fix and the second is not a detail of the first.
func statusOf(rows []row) view.Pair {
	var fails, unknown []string
	for _, r := range rows {
		switch r.grade {
		case gradeFail:
			fails = append(fails, r.headline)
		case gradeWarn:
			unknown = append(unknown, r.headline)
		}
	}
	switch {
	case len(fails) > 0:
		return view.Pair{Key: "status", Value: "fail — " + strings.Join(fails, ", ")}
	case len(unknown) > 0:
		return view.Pair{Key: "status", Value: "warn — " + unknown[0]}
	}
	return view.Pair{Key: "status", Value: "ok"}
}

// couldNotRead is a row whose reading failed: said in the row, in the
// vocabulary the theme draws amber, and carried to the verdict. A missing row
// reads as a healthy one.
func couldNotRead(key string, err error) row {
	return row{
		key:      key,
		value:    "could not be read — " + err.Error(),
		grade:    gradeWarn,
		headline: key + " could not be read",
	}
}

func (f netFacts) routeRow() row {
	r := f.route
	switch {
	case r.err != nil:
		return couldNotRead("route", r.err)
	case !r.found:
		return row{key: "route", value: "fail — no default route", grade: gradeFail, headline: "no default route"}
	}
	var parts []string
	if r.route.gateway != "" {
		parts = append(parts, "via "+r.route.gateway)
	}
	if r.route.iface != "" {
		parts = append(parts, "on "+r.route.iface)
	}
	if len(parts) == 0 {
		return row{key: "route", value: "present"}
	}
	return row{key: "route", value: strings.Join(parts, " ")}
}

func (f netFacts) trafficRow() row {
	if f.traffic.err != nil {
		return couldNotRead("traffic", f.traffic.err)
	}
	return row{key: "traffic", value: f.traffic.rates()}
}

func (f netFacts) dnsRow() row {
	d := f.dns
	switch {
	case d.err != nil:
		return couldNotRead("dns", d.err)
	case len(d.servers) == 0:
		return row{key: "dns", value: "fail — none configured", grade: gradeFail, headline: "no resolver"}
	case len(d.servers) == 1:
		return row{key: "dns", value: d.servers[0]}
	}
	return row{key: "dns", value: fmt.Sprintf("%s (+%d fallback)", d.servers[0], len(d.servers)-1)}
}

func (f netFacts) hostsRow() row {
	h := f.hosts
	if h.err != nil {
		return couldNotRead("hosts", h.err)
	}
	value := fmt.Sprintf("%d custom", h.custom)
	if h.parked > 0 {
		value += fmt.Sprintf(", %d parked", h.parked)
	}
	return row{key: "hosts", value: value}
}

// shownAddrs is how many addresses fit a tile; the rest are counted.
const shownAddrs = 2

// addrRow is the IPv4 addresses, the default route's first. An IPv6-only host
// shows its first IPv6 address instead of nothing; every address of both
// families is on the detailed page.
func (f netFacts) addrRow() row {
	a := f.addrs
	if a.err != nil {
		return couldNotRead("ip", a.err)
	}
	all := a.v4
	if len(all) == 0 && len(a.v6) > 0 {
		all = a.v6[:1]
	}
	switch {
	case len(all) == 0:
		return row{key: "ip", value: "none active"}
	case len(all) <= shownAddrs:
		return row{key: "ip", value: strings.Join(all, " · ")}
	}
	return row{key: "ip", value: fmt.Sprintf("%s (+%d)", strings.Join(all[:shownAddrs], " · "), len(all)-shownAddrs)}
}

// numbers are the figures the rows were made from, for a program (see
// view.KeyValue.Numbers). A reading the page does not show has no number.
func (f netFacts) numbers() map[string]float64 {
	n := map[string]float64{}
	if f.route.err == nil {
		n["default_route"] = 0
		if f.route.found {
			n["default_route"] = 1
		}
	}
	if f.dns.err == nil {
		n["nameservers"] = float64(len(f.dns.servers))
	}
	if f.hosts.err == nil {
		n["hosts_custom"] = float64(f.hosts.custom)
		n["hosts_parked"] = float64(f.hosts.parked)
	}
	if f.traffic.err == nil {
		n["rx_bytes_per_second"] = float64(f.traffic.rx)
		n["tx_bytes_per_second"] = float64(f.traffic.tx)
	}
	return n
}
