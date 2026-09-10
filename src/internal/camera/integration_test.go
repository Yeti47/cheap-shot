package camera

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Yeti47/cheap-shot/src/internal/pb"
	"github.com/Yeti47/cheap-shot/src/internal/pprpc"
)

// fakeCamera answers the handshake the way the firmware does, so the client's
// full connect path can be exercised without hardware.
type fakeCamera struct {
	t          *testing.T
	ln         net.Listener
	password   string
	sessionKey []byte
	rejectAuth bool

	authedUser chan string
	videoPlay  chan struct{}
	wifiSet    chan wifiCreds
	storedSSID string // last SSID set via WifiSet; returned by WifiGet

	framesToSend int
	expected     chan []byte
}

type wifiCreds struct{ ssid, pwd string }

func newFakeCamera(t *testing.T, password string) *fakeCamera {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeCamera{
		t:          t,
		ln:         ln,
		password:   password,
		sessionKey: []byte("0123456789abcdef0123456789abcdef"),
		authedUser: make(chan string, 4),
		videoPlay:  make(chan struct{}, 1),
		wifiSet:    make(chan wifiCreds, 1),
		expected:   make(chan []byte, 8),
	}
	t.Cleanup(func() { ln.Close() })
	go f.serve()
	return f
}

func (f *fakeCamera) addr() (string, int) {
	a := f.ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

func (f *fakeCamera) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeCamera) handle(conn net.Conn) {
	defer conn.Close()
	var buf []byte
	scratch := make([]byte, 4096)
	sentHeartbeats := false
	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err := conn.Read(scratch)
		if n > 0 {
			buf = append(buf, scratch[:n]...)
			packets, rest, perr := pprpc.SplitTCP(buf)
			buf = rest
			if perr != nil {
				return
			}
			for _, p := range packets {
				r, ok := p.(*pprpc.RPC)
				if !ok || r.RPCType != pprpc.RPCRequest {
					continue
				}
				switch r.CommandID {
				case CmdLanAuth:
					if !f.onLanAuth(conn, r) {
						return
					}
				case CmdSyncConn:
					f.respond(conn, r, nil)
					if !sentHeartbeats {
						sentHeartbeats = true
						f.sendHeartbeats(conn)
					}
				case CmdVideoPlay:
					f.respond(conn, r, pb.VarintField(1, 0))
					select {
					case f.videoPlay <- struct{}{}:
					default:
					}
					for i := range f.framesToSend {
						f.expected <- f.sendFrame(conn, uint64(i+1))
					}
				case CmdWifiSet:
					f.onWifiSet(conn, r)
				case CmdWifiGet:
					f.respond(conn, r, pb.StringField(2, f.storedSSID))
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func (f *fakeCamera) onLanAuth(conn net.Conn, r *pprpc.RPC) bool {
	key, iv := pprpc.RPCKey(pprpc.DefaultPrekey, r.Sequence, r.CommandID, uint64(r.RPCType))
	plain, err := pprpc.DecryptCBCPadded(r.Payload, key, iv)
	if err != nil {
		f.t.Errorf("fake camera could not decrypt LanAuth: %v", err)
		return false
	}
	user, _ := pb.FirstBytes(plain, 2)
	pwd, err := pb.FirstBytes(plain, 3)
	if err != nil || string(pwd) != f.password || f.rejectAuth {
		f.respondCode(conn, r, 1, nil)
		return true
	}
	f.authedUser <- string(user)
	f.respond(conn, r, pb.BytesField(1, f.sessionKey))
	return true
}

// onWifiSet mimics dev_on_ipc_WifiSet: decode the request, read ssid (field 2)
// and pwd (field 3), and answer with an empty-body code-0 response.
func (f *fakeCamera) onWifiSet(conn net.Conn, r *pprpc.RPC) {
	key, iv := pprpc.RPCKey(pprpc.DefaultPrekey, r.Sequence, r.CommandID, uint64(r.RPCType))
	plain, err := pprpc.DecryptCBCPadded(r.Payload, key, iv)
	if err != nil {
		f.t.Errorf("fake camera could not decrypt WifiSet: %v", err)
		return
	}
	ssid, _ := pb.FirstBytes(plain, 2)
	pwd, _ := pb.FirstBytes(plain, 3)
	f.storedSSID = string(ssid)
	f.wifiSet <- wifiCreds{ssid: string(ssid), pwd: string(pwd)}
	f.respond(conn, r, nil)
}

func (f *fakeCamera) sendHeartbeats(conn net.Conn) {
	for i := range 2 {
		body := pb.VarintField(1, int64(1000+i))
		frame, err := pprpc.PackRPC(uint64(100+i), CmdConnHB, pprpc.RPCRequest, 0, body, pprpc.DefaultPrekey)
		if err != nil {
			f.t.Error(err)
			return
		}
		conn.Write(frame)
	}
}

func (f *fakeCamera) respond(conn net.Conn, r *pprpc.RPC, body []byte) {
	f.respondCode(conn, r, 0, body)
}

func (f *fakeCamera) respondCode(conn net.Conn, r *pprpc.RPC, code uint64, body []byte) {
	frame, err := pprpc.PackRPC(r.Sequence, r.CommandID, pprpc.RPCResponse, code, body, pprpc.DefaultPrekey)
	if err != nil {
		f.t.Error(err)
		return
	}
	conn.Write(frame)
}

// sendFrame emits one fragmented, head-encrypted MJPEG frame.
func (f *fakeCamera) sendFrame(conn net.Conn, seq uint64) []byte {
	packets, want := buildFrame(f.t, seq, 2048, 1040, 3)
	for _, p := range packets {
		body := []byte{byte(pprpc.FormatMJPEG), 3}
		body = append(body, pb.Varint(p.Channel)...)
		body = append(body, pb.Varint(p.Sequence)...)
		body = append(body, pb.Varint(p.Timestamp)...)
		body = append(body, pb.Varint(p.EncryptedLength)...)
		body = append(body, p.Payload...)
		frame := append([]byte{byte(pprpc.TypeAV<<4 | pprpc.FlagVideoFrag)}, pb.Varint(uint64(len(body)))...)
		conn.Write(append(frame, body...))
	}
	return want
}

func newTestClient(f *fakeCamera, password string, users []string) *Client {
	host, port := f.addr()
	return New(Config{
		Host:           host,
		Port:           port,
		Credentials:    Credentials{Password: password},
		UserCandidates: users,
		ConnectTimeout: 3 * time.Second,
		FrameTimeout:   3 * time.Second,
	})
}

func TestConnectHandshake(t *testing.T) {
	const password = "$L0$deadbeef"
	f := newFakeCamera(t, password)
	c := newTestClient(f, password, []string{"admin"})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(c.SessionKey(), f.sessionKey) {
		t.Fatalf("session key = %q", c.SessionKey())
	}
	if got := <-f.authedUser; got != "admin" {
		t.Fatalf("camera saw user %q", got)
	}
	select {
	case <-f.videoPlay:
	case <-time.After(2 * time.Second):
		t.Fatal("VideoPlay never arrived")
	}
}

func TestSetWiFi(t *testing.T) {
	const password = "$L0$deadbeef"
	f := newFakeCamera(t, password)
	c := newTestClient(f, password, []string{"admin"})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// ConnectControl authenticates without starting a video stream.
	if err := c.ConnectControl(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.videoPlay:
		t.Fatal("ConnectControl must not start a video stream")
	default:
	}

	if err := c.SetWiFi(ctx, "home-net", "s3cret-pass"); err != nil {
		t.Fatalf("SetWiFi: %v", err)
	}
	select {
	case got := <-f.wifiSet:
		if got.ssid != "home-net" || got.pwd != "s3cret-pass" {
			t.Fatalf("camera stored ssid=%q pwd=%q", got.ssid, got.pwd)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("camera never received WifiSet")
	}
}

func TestGetWiFiReadsBack(t *testing.T) {
	const password = "$L0$deadbeef"
	f := newFakeCamera(t, password)
	f.storedSSID = "office-ap"
	c := newTestClient(f, password, []string{"admin"})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.ConnectControl(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetWiFi(ctx)
	if err != nil {
		t.Fatalf("GetWiFi: %v", err)
	}
	if got != "office-ap" {
		t.Fatalf("GetWiFi = %q, want %q", got, "office-ap")
	}
}

func TestSetWiFiRejectsBadInput(t *testing.T) {
	const password = "$L0$deadbeef"
	f := newFakeCamera(t, password)
	c := newTestClient(f, password, []string{"admin"})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.ConnectControl(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.SetWiFi(ctx, "", "pw"); err == nil {
		t.Fatal("empty SSID must be rejected")
	}
	if err := c.SetWiFi(ctx, string(make([]byte, MaxWiFiField+1)), "pw"); err == nil {
		t.Fatal("over-long SSID must be rejected")
	}
}

func TestConnectTriesUserCandidates(t *testing.T) {
	const password = "$L0$deadbeef"
	f := newFakeCamera(t, password)
	// The fake only accepts the password, so every candidate authenticates;
	// asserting on the first one proves the order is honoured.
	c := newTestClient(f, password, []string{"", "did-value", "admin"})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if c.User() != "" {
		t.Fatalf("used user %q, want the first candidate", c.User())
	}
}

func TestConnectRejectedPassword(t *testing.T) {
	f := newFakeCamera(t, "$L0$correct")
	c := newTestClient(f, "$L0$wrong", []string{"admin"})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("got %v, want ErrAuth", err)
	}
}

func TestStreamDeliversFrames(t *testing.T) {
	const password = "$L0$deadbeef"
	f := newFakeCamera(t, password)
	f.framesToSend = 3
	c := newTestClient(f, password, []string{"admin"})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}

	got := make(chan []byte, 8)
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	go func() { c.Stream(streamCtx, func(frame []byte) { got <- frame }) }()

	for i := range f.framesToSend {
		var want []byte
		select {
		case want = <-f.expected:
		case <-time.After(3 * time.Second):
			t.Fatalf("frame %d was never sent by the fake camera", i)
		}
		select {
		case frame := <-got:
			if !bytes.Equal(frame, want) {
				t.Fatalf("frame %d differs: got %d bytes, want %d", i, len(frame), len(want))
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("frame %d never reached the client", i)
		}
	}
}

func TestStreamTimesOutWithoutFrames(t *testing.T) {
	const password = "$L0$deadbeef"
	f := newFakeCamera(t, password)
	c := newTestClient(f, password, []string{"admin"})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	// The fake sends no frames, so the watchdog must fire and trigger a
	// reconnect rather than hanging forever.
	err := c.Stream(ctx, func([]byte) { t.Error("unexpected frame") })
	if err == nil {
		t.Fatal("want a frame-timeout error")
	}
}
