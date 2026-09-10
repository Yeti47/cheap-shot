package httpmjpeg

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Yeti47/cheap-shot/src/internal/frames"
)

func newServer(t *testing.T) (*httptest.Server, *frames.Hub) {
	t.Helper()
	hub := frames.New()
	srv := httptest.NewServer(Handler(map[string]*frames.Hub{"cam1": hub}))
	t.Cleanup(srv.Close)
	return srv, hub
}

func TestSnapshot(t *testing.T) {
	srv, hub := newServer(t)
	hub.Publish([]byte("jpegbytes"))

	resp, err := http.Get(srv.URL + "/cam1/snapshot.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %s", resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content type %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "jpegbytes" {
		t.Fatalf("body %q", body)
	}
}

func TestSnapshotWaitsForFirstFrame(t *testing.T) {
	srv, hub := newServer(t)
	go func() {
		time.Sleep(50 * time.Millisecond)
		hub.Publish([]byte("late"))
	}()
	resp, err := http.Get(srv.URL + "/cam1/snapshot.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "late" {
		t.Fatalf("status %s body %q", resp.Status, body)
	}
}

func TestStreamSendsMultipartParts(t *testing.T) {
	srv, hub := newServer(t)
	hub.Publish([]byte("first"))

	resp, err := http.Get(srv.URL + "/cam1/stream.mjpeg")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/x-mixed-replace") {
		t.Fatalf("content type %q", ct)
	}

	r := bufio.NewReader(resp.Body)
	// Read one multipart part: skip the CRLF left by the previous body and
	// the boundary line, collect headers, then read Content-Length bytes.
	readPart := func() string {
		n := -1
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			line = strings.TrimSpace(line)
			switch {
			case line == "" && n >= 0:
				buf := make([]byte, n)
				if _, err := io.ReadFull(r, buf); err != nil {
					t.Fatalf("read body: %v", err)
				}
				return string(buf)
			case strings.HasPrefix(line, "Content-Length:"):
				n = 0
				for _, c := range strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")) {
					n = n*10 + int(c-'0')
				}
			}
		}
	}
	if got := readPart(); got != "first" {
		t.Fatalf("first part = %q", got)
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		hub.Publish([]byte("second"))
	}()
	if got := readPart(); got != "second" {
		t.Fatalf("second part = %q", got)
	}
}

func TestHealthz(t *testing.T) {
	srv, hub := newServer(t)

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a bridge with no frames should be unhealthy, got %s", resp.Status)
	}
	resp.Body.Close()

	hub.SetConnected(true, "admin", nil)
	hub.Publish([]byte("f"))
	resp, err = http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %s", resp.Status)
	}
	var out map[string]frames.Status
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if s := out["cam1"]; !s.Connected || !s.HasFrame || s.User != "admin" {
		t.Fatalf("status %+v", s)
	}
}

func TestUnknownCameraIs404(t *testing.T) {
	srv, _ := newServer(t)
	resp, err := http.Get(srv.URL + "/nope/snapshot.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %s", resp.Status)
	}
}

func TestDashboard(t *testing.T) {
	srv, _ := newServer(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %s", resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	// the configured camera tile and the live-status script must be present
	for _, want := range []string{`id="tile-cam1"`, `/cam1/stream.mjpeg`, `fetch("/healthz"`, `const CAMS = ["cam1"]`} {
		if !strings.Contains(s, want) {
			t.Fatalf("dashboard missing %q", want)
		}
	}
}
