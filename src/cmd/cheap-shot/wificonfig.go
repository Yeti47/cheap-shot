package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/Yeti47/cheap-shot/src/internal/camera"
	"github.com/Yeti47/cheap-shot/src/internal/config"
)

// runWifiConfig tells a camera to join a router network as a station instead of
// hosting its own AP. It authenticates over the current (AP-side) LAN session
// and sends the WifiSet RPC.
//
// This is deliberately a one-shot subcommand rather than part of `serve`: it
// changes the camera's network membership, so it should be run intentionally.
func runWifiConfig(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("wifi-config", flag.ExitOnError)
	configPath := fs.String("config", "config.json", "path to the bridge configuration")
	camName := fs.String("camera", "", "which configured camera to reconfigure (default: the first)")
	ssid := fs.String("ssid", "", "SSID of the router network the camera should join")
	password := fs.String("password", "", "passphrase for that network (empty for an open network)")
	verbose := fs.Bool("verbose", false, "enable debug logging")
	dryRun := fs.Bool("dry-run", false, "authenticate and report, but do not send WifiSet")
	show := fs.Bool("show", false, "read back and print the SSID the camera currently has stored, then exit")
	timeout := fs.Duration("timeout", 15*time.Second, "overall timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*show {
		if *ssid == "" {
			return errors.New("--ssid is required (or use --show to read back the stored SSID)")
		}
		if len(*ssid) > camera.MaxWiFiField || len(*password) > camera.MaxWiFiField {
			return fmt.Errorf("--ssid and --password must each be at most %d bytes", camera.MaxWiFiField)
		}
	}
	log := newLogger(*verbose)

	cfg, err := config.Load(*configPath, log)
	if err != nil {
		return err
	}
	cam, err := pickCamera(cfg, *camName)
	if err != nil {
		return err
	}
	password0, err := cam.Password()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	client := camera.New(camera.Config{
		Host:           cam.Host,
		Port:           cam.Port,
		Credentials:    camera.Credentials{Prekey: cam.Prekey, User: cam.User, Password: password0},
		UserCandidates: cam.UserCandidates(),
		Logger:         log,
	})
	defer client.Close()

	if err := client.ConnectControl(ctx); err != nil {
		return err
	}
	log.Info("authenticated", "camera", cam.Name, "host", cam.Host, "user", client.User())

	if *show {
		stored, err := client.GetWiFi(ctx)
		if err != nil {
			return err
		}
		if stored == "" {
			fmt.Printf("camera %q has NO station SSID stored (it will stay in AP mode).\n", cam.Name)
		} else {
			fmt.Printf("camera %q has stored station SSID: %q\n", cam.Name, stored)
		}
		return nil
	}

	if *dryRun {
		fmt.Printf("dry run: would set camera %q (%s) to join SSID %q\n", cam.Name, cam.Host, *ssid)
		return nil
	}

	if err := client.SetWiFi(ctx, *ssid, *password); err != nil {
		return err
	}
	fmt.Printf("camera %q accepted the WiFi credentials for SSID %q.\n", cam.Name, *ssid)
	fmt.Println("It is leaving AP mode to join that network; it will get a new IP there via DHCP.")
	fmt.Println("Update the camera's host in your config to that address (a DHCP reservation is easiest),")
	fmt.Printf("then run `cheap-shot serve`. To undo, hold the camera's MODE button ~15s to force AP mode back.\n")
	return nil
}

// pickCamera returns the named camera, or the sole/first one when name is empty.
func pickCamera(cfg *config.Config, name string) (config.Camera, error) {
	if name == "" {
		return cfg.Cameras[0], nil
	}
	for _, c := range cfg.Cameras {
		if c.Name == name {
			return c, nil
		}
	}
	return config.Camera{}, fmt.Errorf("no camera named %q in the configuration", name)
}
