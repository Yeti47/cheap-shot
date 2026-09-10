// Package camera speaks the LAN half of the camera's pprpc protocol: it
// authenticates, keeps the session alive and yields decoded JPEG frames.
package camera

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Yeti47/cheap-shot/src/internal/pb"
	"github.com/Yeti47/cheap-shot/src/internal/pprpc"
)

// RPC command IDs. The names come from the firmware's command table; the
// numeric IDs are the live-validated ones for this protocol family.
const (
	CmdSyncConn  = 106
	CmdConnHB    = 107 // time sync / heartbeat, sent BY the camera
	CmdDiscovery = 2600
	CmdWifiAPGet = 2601 // camera returns a scan of nearby APs
	CmdWifiSet   = 2602 // set the router SSID+password the camera joins as a station
	CmdWifiGet   = 2603 // camera returns the stored station SSID
	CmdVideoPlay = 2610
	CmdAudioPlay = 2614
	CmdLanAuth   = 2650
)

// MaxQoS is the highest QoS the vendor SDK defines. The camera clamps it to
// its own maximum (QoS 5 => 640x480 MJPEG at ~10 fps).
const MaxQoS = 30

// ErrAuth reports that the camera rejected our LanAuth credentials.
var ErrAuth = errors.New("camera: LanAuth rejected")

// Credentials are everything needed to open a LAN session.
type Credentials struct {
	Prekey   string // control-plane key material; defaults to pprpc.DefaultPrekey
	User     string
	Password string // the "$L0$..." value from LanPassword
}

// Config configures one camera connection.
type Config struct {
	Host           string
	Port           int
	Credentials    Credentials
	UserCandidates []string // tried in order when Credentials.User is empty
	ConnectTimeout time.Duration
	FrameTimeout   time.Duration
	DumpDir        string
	Logger         *slog.Logger
}

func (c *Config) withDefaults() {
	if c.Port == 0 {
		c.Port = pprpc.Port
	}
	if c.Credentials.Prekey == "" {
		c.Credentials.Prekey = pprpc.DefaultPrekey
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 5 * time.Second
	}
	if c.FrameTimeout == 0 {
		c.FrameTimeout = 10 * time.Second
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Client is a single LAN session with one camera. It is not safe for
// concurrent use by multiple goroutines.
type Client struct {
	cfg  Config
	conn net.Conn
	buf  []byte
	pend []pprpc.Packet

	sessionKey []byte
	user       string
	seq        uint64 // last RPC sequence number used; allocSeq hands out the next
	dump       *os.File
}

// New returns a client for the given configuration.
func New(cfg Config) *Client {
	cfg.withDefaults()
	return &Client{cfg: cfg}
}

// SessionKey returns the 32-byte hex session key issued by LanAuth.
func (c *Client) SessionKey() []byte { return c.sessionKey }

// User reports which username actually authenticated.
func (c *Client) User() string { return c.user }

// Connect dials the camera and performs the full handshake: LanAuth, the
// connection sync, the camera's initial heartbeat, and VideoPlay.
func (c *Client) Connect(ctx context.Context) error { return c.connect(ctx, true) }

// ConnectControl performs the handshake up to and including the connection
// sync, but does not start a video stream. It is the entry point for control
// commands such as SetWiFi that only need an authenticated session.
func (c *Client) ConnectControl(ctx context.Context) error { return c.connect(ctx, false) }

// connect runs the username-candidate loop, delegating each attempt to
// connectAs. When video is false it stops after the session is established.
func (c *Client) connect(ctx context.Context, video bool) error {
	users := c.cfg.UserCandidates
	if c.cfg.Credentials.User != "" {
		users = []string{c.cfg.Credentials.User}
	}
	if len(users) == 0 {
		users = []string{""}
	}
	var lastErr error
	for _, user := range users {
		err := c.connectAs(ctx, user, video)
		if err == nil {
			c.user = user
			return nil
		}
		c.Close()
		lastErr = err
		if !errors.Is(err, ErrAuth) {
			return err
		}
		c.cfg.Logger.Warn("LanAuth rejected, trying next username candidate", "user", user)
	}
	return lastErr
}

// allocSeq returns the next RPC sequence number. LanAuth and SyncConn take the
// fixed sequences 1 and 2 during the handshake; everything sent afterwards
// (VideoPlay, SetWiFi, ...) draws from here so the numbers never collide.
func (c *Client) allocSeq() uint64 {
	if c.seq < 2 {
		c.seq = 2
	}
	c.seq++
	return c.seq
}

func (c *Client) connectAs(ctx context.Context, user string, video bool) error {
	addr := net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port))
	d := net.Dialer{Timeout: c.cfg.ConnectTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("camera: dial %s: %w", addr, err)
	}
	c.conn, c.buf, c.pend, c.sessionKey, c.seq = conn, nil, nil, nil, 0
	if err := c.openDump(); err != nil {
		return err
	}

	deadline := time.Now().Add(c.cfg.ConnectTimeout)

	// 1. LanAuth -> session key used for every media key afterwards.
	authReq := append(pb.StringField(2, user), pb.StringField(3, c.cfg.Credentials.Password)...)
	if err := c.sendRequest(1, CmdLanAuth, authReq); err != nil {
		return err
	}
	resp, err := c.waitResponse(ctx, 1, CmdLanAuth, deadline)
	if err != nil {
		return err
	}
	plain, err := c.decrypt(resp)
	if err != nil {
		return fmt.Errorf("%w: response undecryptable (%v)", ErrAuth, err)
	}
	key, err := pb.FirstBytes(plain, 1)
	if err != nil {
		return fmt.Errorf("%w: no session key in response", ErrAuth)
	}
	if len(key) != 32 {
		return fmt.Errorf("%w: session key is %d bytes, want 32", ErrAuth, len(key))
	}
	if _, err := hex.DecodeString(string(key)); err != nil {
		return fmt.Errorf("%w: session key is not hex", ErrAuth)
	}
	c.sessionKey = key
	c.cfg.Logger.Info("LanAuth accepted", "host", c.cfg.Host, "user", user)

	// 2. Connection sync.
	if err := c.sendRequest(2, CmdSyncConn, pb.VarintField(1, 1)); err != nil {
		return err
	}
	if _, err := c.waitResponse(ctx, 2, CmdSyncConn, deadline); err != nil {
		return err
	}

	// 3. The camera pushes heartbeat requests of its own; it expects at least
	// one answered before it will start a stream.
	if err := c.awaitHeartbeat(ctx, time.Now().Add(3*time.Second)); err != nil {
		return err
	}

	// 4. Start the stream. The camera clamps QoS to its own maximum. Control
	// sessions (SetWiFi and the like) stop here with an authenticated socket.
	if !video {
		return nil
	}
	seq := c.allocSeq()
	if err := c.sendRequest(seq, CmdVideoPlay, pb.VarintField(2, MaxQoS)); err != nil {
		return err
	}
	if _, err := c.waitResponse(ctx, seq, CmdVideoPlay, time.Now().Add(c.cfg.ConnectTimeout)); err != nil {
		return err
	}
	return nil
}

// Stream delivers complete JPEG frames to onFrame until the context is
// cancelled, the camera goes away, or no frame arrives within FrameTimeout.
func (c *Client) Stream(ctx context.Context, onFrame func([]byte)) error {
	if c.conn == nil || c.sessionKey == nil {
		return errors.New("camera: not connected")
	}
	re := NewReassembler(c.sessionKey)
	frameDeadline := time.Now().Add(c.cfg.FrameTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p, err := c.next(ctx, time.Now().Add(time.Second))
		if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			return err
		}
		switch v := p.(type) {
		case *pprpc.RPC:
			if _, err := c.handleRequest(v); err != nil {
				return err
			}
		case *pprpc.AV:
			if frame := re.Push(v); frame != nil {
				frameDeadline = time.Now().Add(c.cfg.FrameTimeout)
				onFrame(frame)
			}
		}
		if time.Now().After(frameDeadline) {
			return fmt.Errorf("camera: no frame for %s", c.cfg.FrameTimeout)
		}
	}
}

// Close tears down the session.
func (c *Client) Close() error {
	var err error
	if c.conn != nil {
		err = c.conn.Close()
		c.conn = nil
	}
	if c.dump != nil {
		c.dump.Close()
		c.dump = nil
	}
	c.buf, c.pend, c.sessionKey = nil, nil, nil
	return err
}

func (c *Client) sendRequest(seq, cmd uint64, plaintext []byte) error {
	return c.send(seq, cmd, pprpc.RPCRequest, 0, plaintext)
}

func (c *Client) send(seq, cmd uint64, rpcType int, code uint64, plaintext []byte) error {
	if c.conn == nil {
		return errors.New("camera: socket closed")
	}
	frame, err := pprpc.PackRPC(seq, cmd, rpcType, code, plaintext, c.cfg.Credentials.Prekey)
	if err != nil {
		return err
	}
	c.record('S', frame)
	c.conn.SetWriteDeadline(time.Now().Add(c.cfg.ConnectTimeout))
	_, err = c.conn.Write(frame)
	return err
}

func (c *Client) decrypt(p *pprpc.RPC) ([]byte, error) {
	if len(p.Payload) == 0 {
		return nil, nil
	}
	if p.Encryption != pprpc.EncAES256CBC {
		return nil, fmt.Errorf("camera: unsupported RPC encryption %d", p.Encryption)
	}
	key, iv := pprpc.RPCKey(c.cfg.Credentials.Prekey, p.Sequence, p.CommandID, uint64(p.RPCType))
	return pprpc.DecryptCBCPadded(p.Payload, key, iv)
}

// waitResponse pumps packets until the matching response arrives, answering
// heartbeats and stashing media packets on the way.
func (c *Client) waitResponse(ctx context.Context, seq, cmd uint64, deadline time.Time) (*pprpc.RPC, error) {
	for time.Now().Before(deadline) {
		p, err := c.next(ctx, deadline)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			return nil, err
		}
		r, ok := p.(*pprpc.RPC)
		if !ok {
			continue
		}
		if r.RPCType == pprpc.RPCResponse && r.Sequence == seq && r.CommandID == cmd {
			if r.ResponseCode != 0 {
				if cmd == CmdLanAuth {
					return nil, fmt.Errorf("%w: code %d", ErrAuth, r.ResponseCode)
				}
				return nil, fmt.Errorf("camera: command %d returned code %d", cmd, r.ResponseCode)
			}
			return r, nil
		}
		if _, err := c.handleRequest(r); err != nil {
			return nil, err
		}
	}
	if cmd == CmdLanAuth {
		return nil, fmt.Errorf("%w: no response to LanAuth", ErrAuth)
	}
	return nil, fmt.Errorf("camera: no response to command %d", cmd)
}

// awaitHeartbeat waits until at least one camera-initiated heartbeat has been
// answered. The camera sends two; requiring exactly two would be brittle.
func (c *Client) awaitHeartbeat(ctx context.Context, deadline time.Time) error {
	answered := 0
	for time.Now().Before(deadline) && answered < 2 {
		p, err := c.next(ctx, deadline)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			return err
		}
		if r, ok := p.(*pprpc.RPC); ok {
			handled, err := c.handleRequest(r)
			if err != nil {
				return err
			}
			if handled {
				answered++
			}
		}
	}
	if answered == 0 {
		c.cfg.Logger.Warn("camera sent no heartbeat before VideoPlay; continuing anyway")
	}
	return nil
}

// handleRequest answers a camera-initiated heartbeat. It reports whether the
// packet was one.
func (c *Client) handleRequest(r *pprpc.RPC) (bool, error) {
	if r.RPCType != pprpc.RPCRequest || r.CommandID != CmdConnHB {
		return false, nil
	}
	plain, err := c.decrypt(r)
	if err != nil {
		return false, fmt.Errorf("camera: heartbeat undecryptable: %w", err)
	}
	echo, err := pb.FirstVarint(plain, 1)
	if err != nil {
		echo = 0
	}
	body := pb.VarintField(1, int64(echo))
	body = append(body, pb.VarintField(2, -30)...)
	body = append(body, pb.VarintField(3, time.Now().UnixMilli())...)
	if err := c.send(r.Sequence, CmdConnHB, pprpc.RPCResponse, 0, body); err != nil {
		return true, err
	}
	return true, nil
}

// next returns the next packet, reading from the socket when the local queue
// runs dry. It reports os.ErrDeadlineExceeded when nothing arrived in time.
func (c *Client) next(ctx context.Context, deadline time.Time) (pprpc.Packet, error) {
	for {
		if len(c.pend) > 0 {
			p := c.pend[0]
			c.pend = c.pend[1:]
			return p, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !time.Now().Before(deadline) {
			return nil, os.ErrDeadlineExceeded
		}
		if err := c.fill(deadline); err != nil {
			return nil, err
		}
	}
}

func (c *Client) fill(deadline time.Time) error {
	if c.conn == nil {
		return errors.New("camera: socket closed")
	}
	// Cap the blocking read so context cancellation stays responsive.
	read := time.Now().Add(500 * time.Millisecond)
	if deadline.Before(read) {
		read = deadline
	}
	c.conn.SetReadDeadline(read)
	scratch := make([]byte, 64*1024)
	n, err := c.conn.Read(scratch)
	if n > 0 {
		c.record('R', scratch[:n])
		c.buf = append(c.buf, scratch[:n]...)
		packets, rest, perr := pprpc.SplitTCP(c.buf)
		c.pend = append(c.pend, packets...)
		c.buf = rest
		if perr != nil {
			return fmt.Errorf("camera: %w", perr)
		}
	}
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil
		}
		return fmt.Errorf("camera: read: %w", err)
	}
	if n == 0 {
		return errors.New("camera: connection closed")
	}
	return nil
}

func (c *Client) openDump() error {
	if c.cfg.DumpDir == "" {
		return nil
	}
	if err := os.MkdirAll(c.cfg.DumpDir, 0o755); err != nil {
		return err
	}
	name := filepath.Join(c.cfg.DumpDir,
		fmt.Sprintf("%s-%d.pprpc", c.cfg.Host, time.Now().Unix()))
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	c.cfg.Logger.Info("recording raw packets", "file", name)
	c.dump = f
	return nil
}

// record writes one direction-tagged, length-prefixed record to the dump file.
func (c *Client) record(dir byte, b []byte) {
	if c.dump == nil {
		return
	}
	var hdr [5]byte
	hdr[0] = dir
	binary.LittleEndian.PutUint32(hdr[1:], uint32(len(b)))
	c.dump.Write(hdr[:])
	c.dump.Write(b)
}
