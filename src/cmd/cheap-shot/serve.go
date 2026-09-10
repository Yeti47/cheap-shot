package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Yeti47/cheap-shot/src/internal/camera"
	"github.com/Yeti47/cheap-shot/src/internal/config"
	"github.com/Yeti47/cheap-shot/src/internal/frames"
	"github.com/Yeti47/cheap-shot/src/internal/httpmjpeg"
	"github.com/Yeti47/cheap-shot/src/internal/v4l2"
)

const (
	reconnectMin = time.Second
	reconnectMax = 30 * time.Second
)

func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "config.json", "path to the bridge configuration")
	verbose := fs.Bool("verbose", false, "enable debug logging")
	dumpDir := fs.String("dump-packets", "", "directory to record raw pprpc packets into")
	frameTimeout := fs.Duration("frame-timeout", 10*time.Second, "reconnect when no frame arrives for this long")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := newLogger(*verbose)

	cfg, err := config.Load(*configPath, log)
	if err != nil {
		return err
	}

	hubs := make(map[string]*frames.Hub, len(cfg.Cameras))
	sinks := make(map[string]*v4l2.Sink, len(cfg.Cameras))
	defer func() {
		for _, s := range sinks {
			s.Close()
		}
	}()
	for _, cam := range cfg.Cameras {
		hubs[cam.Name] = frames.New()
		if cam.V4L2Device == "" {
			continue
		}
		// Fail loudly at startup: a missing loopback device is a setup
		// problem the user needs to hear about now, not in a log line later.
		sink, err := v4l2.Open(cam.V4L2Device)
		if err != nil {
			return err
		}
		log.Info("v4l2 sink ready", "camera", cam.Name, "device", cam.V4L2Device)
		sinks[cam.Name] = sink
	}

	var wg sync.WaitGroup
	for _, cam := range cfg.Cameras {
		wg.Add(1)
		go func(cam config.Camera) {
			defer wg.Done()
			runCamera(ctx, cam, hubs[cam.Name], sinks[cam.Name], *dumpDir, *frameTimeout, log)
		}(cam)
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           httpmjpeg.Handler(hubs),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("HTTP bridge listening", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-errc:
		return err
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	wg.Wait()
	return nil
}

// runCamera keeps one camera connected, reconnecting with backoff for as long
// as the context lives.
func runCamera(ctx context.Context, cam config.Camera, hub *frames.Hub, sink *v4l2.Sink,
	dumpDir string, frameTimeout time.Duration, log *slog.Logger) {

	camLog := log.With("camera", cam.Name, "host", cam.Host)
	// The LanAuth password is deterministic; resolve it once. A derivation
	// error (bad lslat) is a config problem, not a transient one.
	password, err := cam.Password()
	if err != nil {
		camLog.Error("cannot derive LanAuth password", "err", err)
		return
	}
	backoff := reconnectMin
	for ctx.Err() == nil {
		client := camera.New(camera.Config{
			Host:           cam.Host,
			Port:           cam.Port,
			Credentials:    camera.Credentials{Prekey: cam.Prekey, User: cam.User, Password: password},
			UserCandidates: cam.UserCandidates(),
			FrameTimeout:   frameTimeout,
			DumpDir:        dumpDir,
			Logger:         camLog,
		})
		err := connectAndStream(ctx, client, hub, sink, camLog)
		client.Close()
		if ctx.Err() != nil {
			return
		}
		hub.SetConnected(false, "", err)
		camLog.Warn("camera disconnected", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > reconnectMax {
			backoff = reconnectMax
		}
	}
}

func connectAndStream(ctx context.Context, client *camera.Client, hub *frames.Hub,
	sink *v4l2.Sink, log *slog.Logger) error {

	if err := client.Connect(ctx); err != nil {
		return err
	}
	hub.SetConnected(true, client.User(), nil)
	log.Info("stream started", "session_key_len", len(client.SessionKey()))

	var sinkFailed bool
	return client.Stream(ctx, func(frame []byte) {
		hub.Publish(frame)
		if sink == nil || sinkFailed {
			return
		}
		if err := sink.Write(frame); err != nil {
			// Losing the loopback device must not kill the HTTP stream.
			log.Error("v4l2 write failed; disabling sink for this session", "err", err)
			sinkFailed = true
		}
	})
}
