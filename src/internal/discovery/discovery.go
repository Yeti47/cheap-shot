// Package discovery locates cameras on the local network.
//
// The reliable path is a deliberately slow TCP probe of port 20190: a fast
// full-port sweep is known to wedge this device's TCP/IP stack (see
// docs/findings.md). The UDP broadcast probe is experimental because the
// firmware's Discovery command ID has not been recovered yet.
package discovery

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/Yeti47/cheap-shot/src/internal/pb"
	"github.com/Yeti47/cheap-shot/src/internal/pprpc"
)

// APGateway is the address the camera serves in its own factory AP mode.
const APGateway = "192.168.9.252"

// DefaultDiscoveryCmd is a working hypothesis, not a confirmed value: the
// firmware's command-name table is ordered and places Discovery four slots
// before VideoPlay (2610). Override it if the probe stays silent.
const DefaultDiscoveryCmd = 2606

// ClassCode is what the firmware requires in a discovery request; it logs
// "Discovery input class_code:%s, != IPAV" for anything else.
const ClassCode = "IPAV"

// Result is one camera candidate.
type Result struct {
	IP   string
	How  string // "tcp" or "udp"
	Note string
}

// ScanOptions tunes the TCP probe.
type ScanOptions struct {
	Timeout     time.Duration // per-host connect timeout
	Concurrency int
	Pace        time.Duration // delay between dials, to stay gentle
}

func (o *ScanOptions) withDefaults() {
	if o.Timeout == 0 {
		o.Timeout = 400 * time.Millisecond
	}
	if o.Concurrency == 0 {
		o.Concurrency = 8
	}
	if o.Pace == 0 {
		o.Pace = 10 * time.Millisecond
	}
}

// Scan probes port 20190 across every host of the given IPv4 /24s. When
// networks is empty the local interfaces' own /24s are used, and the camera's
// factory AP address is tried first.
func Scan(ctx context.Context, networks []string, opts ScanOptions) ([]Result, error) {
	opts.withDefaults()
	hosts, err := hostList(networks)
	if err != nil {
		return nil, err
	}

	var (
		mu      sync.Mutex
		results []Result
		wg      sync.WaitGroup
	)
	sem := make(chan struct{}, opts.Concurrency)
	for _, host := range hosts {
		if ctx.Err() != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			defer func() { <-sem }()
			if probeTCP(ctx, host, opts.Timeout) {
				mu.Lock()
				results = append(results, Result{IP: host, How: "tcp", Note: "port 20190 open"})
				mu.Unlock()
			}
		}(host)
		time.Sleep(opts.Pace)
	}
	wg.Wait()
	return results, ctx.Err()
}

func probeTCP(ctx context.Context, host string, timeout time.Duration) bool {
	return probeTCPPort(ctx, host, pprpc.Port, timeout)
}

func probeTCPPort(ctx context.Context, host string, port int, timeout time.Duration) bool {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// hostList expands the networks (or the local interfaces) into host addresses,
// with the factory AP gateway first when it is in range.
func hostList(networks []string) ([]string, error) {
	var nets []*net.IPNet
	if len(networks) > 0 {
		for _, n := range networks {
			_, ipnet, err := net.ParseCIDR(n)
			if err != nil {
				return nil, fmt.Errorf("discovery: %q: %w", n, err)
			}
			nets = append(nets, ipnet)
		}
	} else {
		var err error
		if nets, err = localNets(); err != nil {
			return nil, err
		}
	}

	seen := map[string]bool{}
	var hosts []string
	add := func(ip string) {
		if !seen[ip] {
			seen[ip] = true
			hosts = append(hosts, ip)
		}
	}
	for _, n := range nets {
		if n.Contains(net.ParseIP(APGateway)) {
			add(APGateway)
		}
	}
	for _, n := range nets {
		ones, bits := n.Mask.Size()
		if bits != 32 || ones < 24 {
			return nil, fmt.Errorf("discovery: %s is wider than a /24; pass an explicit network", n)
		}
		base := n.IP.Mask(n.Mask).To4()
		count := 1 << (32 - ones)
		for i := 1; i < count-1; i++ {
			ip := make(net.IP, 4)
			copy(ip, base)
			v := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
			v += uint32(i)
			ip[0], ip[1], ip[2], ip[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
			add(ip.String())
		}
	}
	return hosts, nil
}

func localNets() ([]*net.IPNet, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []*net.IPNet
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			if ones, _ := ipnet.Mask.Size(); ones < 24 {
				continue // too wide to walk politely
			}
			out = append(out, &net.IPNet{IP: ipnet.IP.Mask(ipnet.Mask), Mask: ipnet.Mask})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("discovery: no suitable IPv4 network found; pass one explicitly")
	}
	return out, nil
}

// ProbeUDP broadcasts a Discovery request on UDP 20190 and collects replies.
// Experimental: the command ID is unconfirmed, so silence here is not proof
// that no camera is present -- fall back to Scan.
func ProbeUDP(ctx context.Context, cmdID uint64, prekey string, wait time.Duration) ([]Result, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	if err != nil {
		return nil, fmt.Errorf("discovery: udp listen: %w", err)
	}
	defer conn.Close()

	body := pb.StringField(1, ClassCode)
	frame, err := pprpc.PackRPC(1, cmdID, pprpc.RPCRequest, 0, body, prekey)
	if err != nil {
		return nil, err
	}
	packet := pprpc.PackUDP(frame)

	for _, target := range broadcastTargets() {
		conn.WriteToUDP(packet, &net.UDPAddr{IP: target, Port: pprpc.Port})
	}

	deadline := time.Now().Add(wait)
	conn.SetReadDeadline(deadline)
	seen := map[string]bool{}
	var results []Result
	buf := make([]byte, 64*1024)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		ip := addr.IP.String()
		if seen[ip] {
			continue
		}
		seen[ip] = true
		note := fmt.Sprintf("%d byte reply", n)
		udp := true
		if p, err := pprpc.Parse(buf[:n], &udp); err == nil {
			note = fmt.Sprintf("%s, type %d", note, p.Head().Type)
		}
		results = append(results, Result{IP: ip, How: "udp", Note: note})
	}
	return results, nil
}

func broadcastTargets() []net.IP {
	targets := []net.IP{net.IPv4bcast}
	nets, err := localNets()
	if err != nil {
		return targets
	}
	for _, n := range nets {
		ip := n.IP.Mask(n.Mask).To4()
		if ip == nil {
			continue
		}
		b := make(net.IP, 4)
		for i := range b {
			b[i] = ip[i] | ^n.Mask[i]
		}
		targets = append(targets, b)
	}
	return targets
}
