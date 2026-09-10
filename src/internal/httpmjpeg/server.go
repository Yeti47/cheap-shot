// Package httpmjpeg re-serves camera frames as MJPEG over HTTP, plus
// single-frame snapshots and a health endpoint.
package httpmjpeg

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Yeti47/cheap-shot/src/internal/frames"
)

const boundary = "cheapshotframe"

// snapshotWait is how long a snapshot request waits for the first frame after
// a cold start.
const snapshotWait = 5 * time.Second

// Handler serves the bridge's HTTP endpoints for a set of named cameras.
func Handler(hubs map[string]*frames.Hub) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		out := make(map[string]frames.Status, len(hubs))
		healthy := false
		for name, h := range hubs {
			s := h.Status()
			out[name] = s
			if s.Connected && s.HasFrame {
				healthy = true
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if !healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("GET /{$}", dashboardHandler(hubs))
	mux.HandleFunc("GET /{cam}/snapshot.jpg", withHub(hubs, snapshot))
	mux.HandleFunc("GET /{cam}/stream.mjpeg", withHub(hubs, stream))
	return mux
}

func withHub(hubs map[string]*frames.Hub, fn func(http.ResponseWriter, *http.Request, *frames.Hub)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hub, ok := hubs[r.PathValue("cam")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fn(w, r, hub)
	}
}

func snapshot(w http.ResponseWriter, r *http.Request, hub *frames.Hub) {
	frame, ok := hub.Latest()
	if !ok {
		// Cold start: wait briefly for the first frame rather than 503-ing.
		ch, cancel := hub.Subscribe()
		defer cancel()
		select {
		case f, alive := <-ch:
			if !alive {
				http.Error(w, "camera has no frame", http.StatusServiceUnavailable)
				return
			}
			frame = f
		case <-time.After(snapshotWait):
			http.Error(w, "camera has no frame", http.StatusServiceUnavailable)
			return
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", fmt.Sprint(len(frame)))
	w.Header().Set("Cache-Control", "no-store")
	w.Write(frame)
}

func stream(w http.ResponseWriter, r *http.Request, hub *frames.Hub) {
	ch, cancel := hub.Subscribe()
	defer cancel()

	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+boundary)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	// Send the frame we already have so the viewer sees something at once.
	if frame, ok := hub.Latest(); ok {
		if err := writePart(w, frame); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	for {
		select {
		case frame, alive := <-ch:
			if !alive {
				return
			}
			if err := writePart(w, frame); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

func writePart(w http.ResponseWriter, frame []byte) error {
	_, err := fmt.Fprintf(w, "--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", boundary, len(frame))
	if err != nil {
		return err
	}
	if _, err = w.Write(frame); err != nil {
		return err
	}
	_, err = w.Write([]byte("\r\n"))
	return err
}
