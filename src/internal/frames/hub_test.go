package frames

import (
	"errors"
	"testing"
	"time"
)

func TestPublishReachesSubscribers(t *testing.T) {
	h := New()
	ch, cancel := h.Subscribe()
	defer cancel()

	h.Publish([]byte("frame"))
	select {
	case got := <-ch:
		if string(got) != "frame" {
			t.Fatalf("got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber received nothing")
	}
	if frame, ok := h.Latest(); !ok || string(frame) != "frame" {
		t.Fatalf("Latest = %q, %v", frame, ok)
	}
}

func TestSlowSubscriberDoesNotBlockPublisher(t *testing.T) {
	h := New()
	_, cancel := h.Subscribe() // never drained
	defer cancel()

	done := make(chan struct{})
	go func() {
		for range subscriberBuffer + 10 {
			h.Publish([]byte("f"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that stopped reading")
	}
}

func TestUnsubscribeClosesChannel(t *testing.T) {
	h := New()
	ch, cancel := h.Subscribe()
	cancel()
	if _, alive := <-ch; alive {
		t.Fatal("channel should be closed after cancel")
	}
	cancel() // must be idempotent
}

func TestStatus(t *testing.T) {
	h := New()
	s := h.Status()
	if s.Connected || s.HasFrame {
		t.Fatalf("fresh hub reports %+v", s)
	}
	h.SetConnected(false, "", errors.New("boom"))
	if got := h.Status().Error; got != "boom" {
		t.Fatalf("error = %q", got)
	}
	h.Publish([]byte("f"))
	h.Publish([]byte("f"))
	s = h.Status()
	if !s.Connected || !s.HasFrame || s.Frames != 2 || s.Error != "" {
		t.Fatalf("after frames: %+v", s)
	}
}
