package net

import "testing"

const procRoute = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
	"eth0\t00000000\t0102A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
	"eth0\t0002A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n" +
	"docker0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"

func TestTheDefaultRouteOfALinuxIPv4Table(t *testing.T) {
	got, ok := parseProcNetRoute(procRoute)
	if !ok || got.gateway != "192.168.2.1" || got.iface != "eth0" {
		t.Errorf("default route = %+v, %v; want 192.168.2.1 on eth0", got, ok)
	}
}

// Of several, the kernel takes the cheapest, and a route that is down is no
// route; a point-to-point default (a tunnel) has no gateway and is still the way
// out.
func TestTheCheapestUpDefaultRouteWins(t *testing.T) {
	table := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"wlan0\t00000000\t0101A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n" +
		"eth0\t00000000\t0102A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"eth1\t00000000\t0103A8C0\t0002\t0\t0\t10\t00000000\t0\t0\t0\n"
	got, ok := parseProcNetRoute(table)
	if !ok || got.iface != "eth0" || got.gateway != "192.168.2.1" {
		t.Errorf("default route = %+v, %v; want eth0 (cheapest of the ones that are up)", got, ok)
	}

	tunnel := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"wg0\t00000000\t00000000\t0001\t0\t0\t0\t00000000\t0\t0\t0\n"
	got, ok = parseProcNetRoute(tunnel)
	if !ok || got.iface != "wg0" || got.gateway != "" {
		t.Errorf("default route = %+v, %v; want wg0 with no gateway", got, ok)
	}
}

func TestATableWithNoDefaultRouteHasNone(t *testing.T) {
	for name, table := range map[string]string{
		"empty":       "",
		"header only": "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n",
		"only local":  "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\neth0\t0002A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n",
		"garbage":     "not a routing table\nat all\n",
		"short line":  "eth0\t00000000\t0102A8C0\n",
	} {
		if got, ok := parseProcNetRoute(table); ok {
			t.Errorf("%s: found %+v", name, got)
		}
	}
}

// The IPv6 table is fixed-width hexadecimal, and holds an unreachable default on
// the loopback that is the kernel saying there is no route.
func TestTheDefaultRouteOfALinuxIPv6Table(t *testing.T) {
	const zero = "00000000000000000000000000000000"
	table := zero + " 00 " + zero + " 00 " + zero + " ffffffff 00000001 00000000 00200200 lo\n" +
		"fe800000000000000000000000000000 40 " + zero + " 00 " + zero + " 00000100 00000001 00000000 00000001 eth0\n" +
		zero + " 00 " + zero + " 00 fe80000000000000021122fffe334455 00000400 00000001 00000000 00000003 eth0\n"
	got, ok := parseProcNetIPv6Route(table)
	if !ok || got.gateway != "fe80::211:22ff:fe33:4455" || got.iface != "eth0" {
		t.Errorf("default route = %+v, %v; want fe80::211:22ff:fe33:4455 on eth0", got, ok)
	}

	if got, ok := parseProcNetIPv6Route(zero + " 00 " + zero + " 00 " + zero + " ffffffff 00000001 00000000 00200200 lo\n"); ok {
		t.Errorf("the loopback's unreachable default was taken for a route: %+v", got)
	}
	if got, ok := parseProcNetIPv6Route(""); ok {
		t.Errorf("an empty table has the route %+v", got)
	}
}
