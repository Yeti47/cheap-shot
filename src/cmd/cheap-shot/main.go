// Command cheap-shot turns a stock "A9-style" BK7252 mini camera into an
// ordinary Linux webcam: it speaks the camera's own pprpc protocol on the LAN
// and re-serves the stream as MJPEG over HTTP and as a /dev/video* device.
//
// It never talks to the vendor cloud and needs no vendor account.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Yeti47/cheap-shot/src/internal/camera"
	"github.com/Yeti47/cheap-shot/src/internal/discovery"
)

const usage = `cheap-shot - local bridge for A9-style BK7252 WiFi cameras

usage:
  cheap-shot serve       --config config.json      run the bridge
  cheap-shot discover    [--udp] [--net CIDR]      find cameras on the LAN
  cheap-shot derive      --did D --scode S         print the LanAuth password
  cheap-shot wifi-config --ssid S --password P     make the camera join a WiFi network
  cheap-shot healthcheck [--url URL]               probe a running bridge

Run any subcommand with -h for its flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe(ctx, os.Args[2:])
	case "discover":
		err = runDiscover(ctx, os.Args[2:])
	case "derive":
		err = runDerive(os.Args[2:])
	case "wifi-config":
		err = runWifiConfig(ctx, os.Args[2:])
	case "healthcheck":
		err = runHealthcheck(ctx, os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		if ctx.Err() != nil {
			return // a clean shutdown, not a failure
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newLogger(verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func runDerive(args []string) error {
	fs := flag.NewFlagSet("derive", flag.ExitOnError)
	did := fs.String("did", "", "device id (the factory record's PRODUCT_KEY)")
	scode := fs.String("scode", "", "security code (the factory record's PRODUCT_SECRET)")
	idx := fs.Int("index", 0, "auth index")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *did == "" || *scode == "" {
		return fmt.Errorf("both --did and --scode are required")
	}
	fmt.Println(camera.LanPassword(*did, *scode, *idx))
	return nil
}

func runDiscover(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	useUDP := fs.Bool("udp", false, "also try the experimental UDP broadcast probe")
	cmdID := fs.Uint64("discovery-cmd", discovery.DefaultDiscoveryCmd, "RPC command id for the UDP probe")
	network := fs.String("net", "", "IPv4 network to scan, e.g. 192.168.9.0/24 (default: local interfaces)")
	timeout := fs.Duration("timeout", 400*time.Millisecond, "per-host TCP connect timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var found []discovery.Result
	if *useUDP {
		results, err := discovery.ProbeUDP(ctx, *cmdID, "", 2*time.Second)
		if err != nil {
			fmt.Fprintln(os.Stderr, "udp probe:", err)
		}
		found = append(found, results...)
	}

	var networks []string
	if *network != "" {
		networks = []string{*network}
	}
	fmt.Fprintln(os.Stderr, "scanning port 20190 (slow on purpose - a fast sweep wedges this device)...")
	results, err := discovery.Scan(ctx, networks, discovery.ScanOptions{Timeout: *timeout})
	if err != nil && ctx.Err() == nil {
		return err
	}
	found = append(found, results...)

	if len(found) == 0 {
		fmt.Println("no cameras found")
		return nil
	}
	for _, r := range found {
		fmt.Printf("%-15s  %-4s  %s\n", r.IP, r.How, r.Note)
	}
	return nil
}

func runHealthcheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080/healthz", "health endpoint to probe")
	if err := fs.Parse(args); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: %s", resp.Status)
	}
	return nil
}
