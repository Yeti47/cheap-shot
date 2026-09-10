package camera

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Yeti47/cheap-shot/src/internal/pb"
)

// MaxWiFiField is the largest SSID or passphrase the firmware's WifiSet
// request struct can hold: the nanopb message stores each as a fixed char[65]
// (64 bytes + NUL), so anything longer would be truncated by the camera.
const MaxWiFiField = 64

// SetWiFi hands the camera the SSID and passphrase of a router network to join
// as a station, using the WifiSet RPC (command 2602). On success the camera
// stores the credentials and switches out of its own AP into station mode --
// which drops this AP-side session, so a nil return followed by the connection
// going away is the expected, successful outcome.
//
// The request mirrors LanAuth's field convention (field 1 is the connection
// channel, the payload begins at field 2): field 2 = ssid, field 3 = passwd.
// Recovered from the command registration table (req descriptor at logical
// 0x142624: f1 int, f2 string[65], f3 string[65]) and the dev_on_ipc_WifiSet
// handler (0x79770), which logs "ssid:%s"/"pwd:%s" and then "wifi verify set
// true". The response body is empty; success is response code 0.
//
// The session must already be authenticated -- call ConnectControl first.
func (c *Client) SetWiFi(ctx context.Context, ssid, password string) error {
	if c.conn == nil || c.sessionKey == nil {
		return errors.New("camera: not connected")
	}
	if ssid == "" {
		return errors.New("camera: SSID must not be empty")
	}
	if len(ssid) > MaxWiFiField {
		return fmt.Errorf("camera: SSID is %d bytes, the firmware truncates past %d", len(ssid), MaxWiFiField)
	}
	if len(password) > MaxWiFiField {
		return fmt.Errorf("camera: passphrase is %d bytes, the firmware truncates past %d", len(password), MaxWiFiField)
	}

	body := append(pb.StringField(2, ssid), pb.StringField(3, password)...)
	seq := c.allocSeq()
	if err := c.sendRequest(seq, CmdWifiSet, body); err != nil {
		return err
	}
	if _, err := c.waitResponse(ctx, seq, CmdWifiSet, time.Now().Add(c.cfg.ConnectTimeout)); err != nil {
		return err
	}
	c.cfg.Logger.Info("WifiSet accepted; camera will join the network as a station and leave AP mode",
		"ssid", ssid, "host", c.cfg.Host)
	return nil
}

// GetWiFi reads back the station SSID the camera currently has stored, via the
// WifiGet RPC (command 2603, response descriptor at logical 0x1425c0 whose
// field 2 is the SSID string). It is the read-back counterpart to SetWiFi: an
// empty result means the camera has no station credentials stored (it will stay
// in AP mode), and a value different from what you set means SetWiFi did not
// land. The session must already be authenticated (call ConnectControl first).
func (c *Client) GetWiFi(ctx context.Context) (string, error) {
	if c.conn == nil || c.sessionKey == nil {
		return "", errors.New("camera: not connected")
	}
	// The request carries only the (optional) channel field; an empty body
	// decodes to channel 0, which is what we want.
	seq := c.allocSeq()
	if err := c.sendRequest(seq, CmdWifiGet, nil); err != nil {
		return "", err
	}
	resp, err := c.waitResponse(ctx, seq, CmdWifiGet, time.Now().Add(c.cfg.ConnectTimeout))
	if err != nil {
		return "", err
	}
	plain, err := c.decrypt(resp)
	if err != nil {
		return "", fmt.Errorf("camera: WifiGet response undecryptable: %w", err)
	}
	ssid, err := pb.FirstBytes(plain, 2)
	if err != nil {
		return "", nil // no SSID field => nothing stored
	}
	return string(ssid), nil
}
