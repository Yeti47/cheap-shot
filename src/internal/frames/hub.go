// Package frames fans one camera's JPEG stream out to any number of
// consumers without letting a slow consumer stall the camera reader.
package frames

import (
	"sync"
	"time"
)

const (
	subscriberBuffer = 2
	fpsWindow        = 30
)

// Status is a point-in-time summary for the health endpoint.
type Status struct {
	Connected   bool      `json:"connected"`
	HasFrame    bool      `json:"has_frame"`
	Frames      uint64    `json:"frames"`
	FPS         float64   `json:"fps"`
	LastFrameAt time.Time `json:"last_frame_at,omitzero"`
	Error       string    `json:"error,omitempty"`
	User        string    `json:"user,omitempty"`
}

// Hub holds the latest frame and broadcasts new ones to subscribers.
type Hub struct {
	mu        sync.Mutex
	latest    []byte
	latestAt  time.Time
	count     uint64
	recent    []time.Time
	subs      map[chan []byte]struct{}
	connected bool
	lastErr   string
	user      string
}

// New returns an empty hub.
func New() *Hub { return &Hub{subs: map[chan []byte]struct{}{}} }

// Publish stores a frame and hands it to every subscriber, dropping the frame
// for any subscriber that is not keeping up.
func (h *Hub) Publish(frame []byte) {
	h.mu.Lock()
	h.latest = frame
	h.latestAt = time.Now()
	h.count++
	h.connected = true
	h.lastErr = ""
	h.recent = append(h.recent, h.latestAt)
	if len(h.recent) > fpsWindow {
		h.recent = h.recent[len(h.recent)-fpsWindow:]
	}
	subs := make([]chan []byte, 0, len(h.subs))
	for ch := range h.subs {
		subs = append(subs, ch)
	}
	h.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- frame:
		default: // subscriber is behind; skip this frame for it
		}
	}
}

// Latest returns the most recent frame, if any.
func (h *Hub) Latest() ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.latest, h.latest != nil
}

// Subscribe returns a channel of frames and a function to unsubscribe.
func (h *Hub) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, subscriberBuffer)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// SetConnected records connection state and the last error, if any.
func (h *Hub) SetConnected(connected bool, user string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.connected = connected
	h.user = user
	if err != nil {
		h.lastErr = err.Error()
	} else if connected {
		h.lastErr = ""
	}
}

// Status snapshots the hub for reporting.
func (h *Hub) Status() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := Status{
		Connected:   h.connected,
		HasFrame:    h.latest != nil,
		Frames:      h.count,
		LastFrameAt: h.latestAt,
		Error:       h.lastErr,
		User:        h.user,
	}
	if n := len(h.recent); n >= 2 {
		if span := h.recent[n-1].Sub(h.recent[0]).Seconds(); span > 0 {
			s.FPS = float64(n-1) / span
		}
	}
	return s
}
