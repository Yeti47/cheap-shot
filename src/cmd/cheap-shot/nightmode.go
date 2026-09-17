package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/Yeti47/cheap-shot/src/internal/camera"
	"github.com/Yeti47/cheap-shot/src/internal/config"
)

// runNightMode reads or sets the camera's day/night mode (the IRCutSet /
// IRCutGet RPCs).
//
// Like wifi-config this is a one-shot subcommand holding its own authenticated
// session, because the camera accepts only ONE authenticated LAN session at a
// time: a second LanAuth while `serve` is running is refused with the socket
// closed. Stop the bridge before running this, then start it again. The mode
// lives in the camera's RAM, so it survives the session ending -- but resets to
// "day" on every camera reboot.
func runNightMode(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("night-mode", flag.ExitOnError)
	configPath := fs.String("config", "config.json", "path to the bridge configuration")
	camName := fs.String("camera", "", "which configured camera to address (default: the first)")
	mode := fs.String("mode", "", "day, night or auto (or the raw firmware numbers 2, 1, 3)")
	show := fs.Bool("show", false, "print the mode the camera currently has, then exit")
	verbose := fs.Bool("verbose", false, "enable debug logging")
	timeout := fs.Duration("timeout", 15*time.Second, "overall timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *mode == "" && !*show {
		return fmt.Errorf("--mode is required (day, night or auto), or use --show to read the current mode")
	}
	var want camera.DayNightMode
	if *mode != "" {
		var err error
		if want, err = camera.ParseDayNightMode(*mode); err != nil {
			return err
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
	password, err := cam.Password()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	client := camera.New(camera.Config{
		Host:           cam.Host,
		Port:           cam.Port,
		Credentials:    camera.Credentials{Prekey: cam.Prekey, User: cam.User, Password: password},
		UserCandidates: cam.UserCandidates(),
		Logger:         log,
	})
	defer client.Close()

	if err := client.ConnectControl(ctx); err != nil {
		return fmt.Errorf("%w (is `cheap-shot serve` still running? the camera allows only one session)", err)
	}
	log.Info("authenticated", "camera", cam.Name, "host", cam.Host, "user", client.User())

	current, err := client.GetIRCut(ctx)
	if err != nil {
		return err
	}
	if *show {
		fmt.Printf("camera %q day/night mode: %s (%d)\n", cam.Name, current, int64(current))
		return nil
	}

	if err := client.SetIRCut(ctx, want); err != nil {
		return err
	}
	// Read back rather than trusting the empty ack: the handler stores the
	// value without validating it, so the read-back is the only confirmation
	// that what we sent is what the camera is now acting on.
	got, err := client.GetIRCut(ctx)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("camera %q reports mode %s after being set to %s", cam.Name, got, want)
	}
	fmt.Printf("camera %q day/night mode: %s -> %s\n", cam.Name, current, got)
	if want == camera.DayNightNight || want == camera.DayNightAuto {
		fmt.Println("Night mode is grayscale plus a GPIO; it changes no exposure or gain setting,")
		fmt.Println("and on our unit nothing illuminates. See docs/findings.md.")
	}
	fmt.Println("The mode is held in RAM and resets to \"day\" when the camera reboots.")
	return nil
}
