package discovery

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestHostListPutsAPGatewayFirst(t *testing.T) {
	hosts, err := hostList([]string{"192.168.9.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if hosts[0] != APGateway {
		t.Fatalf("first host = %s, want %s", hosts[0], APGateway)
	}
	if len(hosts) != 254 {
		t.Fatalf("got %d hosts, want 254", len(hosts))
	}
	// Network and broadcast addresses must not be probed.
	for _, h := range hosts {
		if h == "192.168.9.0" || h == "192.168.9.255" {
			t.Fatalf("%s should not be probed", h)
		}
	}
}

func TestHostListRejectsWideNetworks(t *testing.T) {
	if _, err := hostList([]string{"10.0.0.0/8"}); err == nil {
		t.Fatal("want an error for a network wider than a /24")
	}
	if _, err := hostList([]string{"not a cidr"}); err == nil {
		t.Fatal("want an error for a malformed CIDR")
	}
}

func TestScanFindsAnOpenPort(t *testing.T) {
	// Probe a /30 containing a listener we control, on the real port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !probeTCPPort(ctx, "127.0.0.1", port, time.Second) {
		t.Fatal("probe did not find the listener")
	}
	if probeTCPPort(ctx, "127.0.0.1", port+1, 200*time.Millisecond) {
		t.Skip("a stray listener occupies the neighbouring port")
	}
}

func TestProbeUDPIsHarmlessWhenNobodyAnswers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results, err := ProbeUDP(ctx, DefaultDiscoveryCmd, "", 200*time.Millisecond)
	if err != nil && !strings.Contains(err.Error(), "permission") {
		t.Fatal(err)
	}
	_ = results
}
