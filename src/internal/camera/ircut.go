package camera

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Yeti47/cheap-shot/src/internal/pb"
)

// DayNightMode is the camera's day/night setting, carried by IRCutSet/IRCutGet.
//
// The name is the firmware's, not a description: despite "IRCut" there is no
// mechanical IR-cut filter on this hardware, and on our unit nothing lights up
// either. See SetIRCut for what the modes actually do.
type DayNightMode int64

const (
	// DayNightNight forces night mode: grayscale, and GPIO 7 driven high.
	DayNightNight DayNightMode = 1
	// DayNightDay forces day mode: normal colour, GPIO 7 low. This is the
	// camera's state after every boot.
	DayNightDay DayNightMode = 2
	// DayNightAuto switches on the camera's own clock: hours 7..17 inclusive
	// are day, everything else is night. The camera has no internet, but the
	// bridge answers its ConnHB heartbeat with our wall-clock time, so its
	// clock is correct while we are connected.
	DayNightAuto DayNightMode = 3
)

func (m DayNightMode) String() string {
	switch m {
	case DayNightNight:
		return "night"
	case DayNightDay:
		return "day"
	case DayNightAuto:
		return "auto"
	default:
		return fmt.Sprintf("unknown(%d)", int64(m))
	}
}

// Valid reports whether m is one of the three modes the firmware acts on.
//
// The handler stores whatever it is given without validating it, and any other
// value simply matches none of the task's branches -- leaving the camera in
// whatever mode it was already in, with no error to tell you so.
func (m DayNightMode) Valid() bool {
	return m == DayNightNight || m == DayNightDay || m == DayNightAuto
}

// ParseDayNightMode accepts either a mode name ("day", "night", "auto") or the
// raw firmware number ("1", "2", "3").
func ParseDayNightMode(s string) (DayNightMode, error) {
	switch s {
	case "night", "1":
		return DayNightNight, nil
	case "day", "2":
		return DayNightDay, nil
	case "auto", "3":
		return DayNightAuto, nil
	}
	return 0, fmt.Errorf("camera: %q is not a day/night mode (want day, night or auto)", s)
}

// SetIRCut sets the camera's day/night mode via the IRCutSet RPC (command
// 2635). The session must already be authenticated -- call ConnectControl
// first.
//
// Request `{f1 channel:int32, f2 mode:int32}` (descriptor at logical 0x140b48),
// empty response, code 0 = accepted -- the same shape as WifiSet. Field 1 is
// the connection channel every message carries, so the payload starts at 2.
//
// What the modes do, recovered from the dump and confirmed on hardware:
// dev_on_ipc_IRCutSet (0x7a640) only stores the mode in a global; a 200 ms task
// (0x833ac) reads it back through 0x794d0 and acts, calling
// camera_intf_set_day_night (0xc4f80) and driving GPIO 7. For our sensor
// (GC0310/GC0312, code 0x64, branch at 0xc5208) "night" writes sensor registers
// 0xd1/0xd2 -- Cb/Cr saturation -- to 0, and "day" restores them to 0x34.
//
// So night mode is grayscale plus a GPIO. It does NOT touch exposure, gain,
// AEC target or frame rate: those are written once from the boot init table at
// 0x164732 and never again (all 191 sensor register writes in the image live in
// the flip, resolution, FPS, init and day/night functions). Measured live on
// our unit, switching to night dropped chroma to zero and left mean luma
// unchanged, so there is no working illuminator on GPIO 7 here either.
//
// The mode lives in RAM: it survives this session ending but resets to
// DayNightDay on every camera reboot.
func (c *Client) SetIRCut(ctx context.Context, mode DayNightMode) error {
	if c.conn == nil || c.sessionKey == nil {
		return errors.New("camera: not connected")
	}
	if !mode.Valid() {
		return fmt.Errorf("camera: day/night mode %d is not one of 1 (night), 2 (day), 3 (auto); "+
			"the firmware would store it and act on none of them", int64(mode))
	}

	seq := c.allocSeq()
	if err := c.sendRequest(seq, CmdIRCutSet, pb.VarintField(2, int64(mode))); err != nil {
		return err
	}
	if _, err := c.waitResponse(ctx, seq, CmdIRCutSet, time.Now().Add(c.cfg.ConnectTimeout)); err != nil {
		return err
	}
	c.cfg.Logger.Info("IRCutSet accepted", "mode", mode.String(), "host", c.cfg.Host)
	return nil
}

// GetIRCut reads the camera's current day/night mode via the IRCutGet RPC
// (command 2636, request carries only the channel field, response f1 is the
// mode). It reads the same global SetIRCut writes, through the same handler
// pair (0x7a680 reads what 0x7a640 stored), so it is a genuine read-back rather
// than a separate setting. A freshly booted camera answers DayNightDay.
//
// The session must already be authenticated -- call ConnectControl first.
func (c *Client) GetIRCut(ctx context.Context) (DayNightMode, error) {
	if c.conn == nil || c.sessionKey == nil {
		return 0, errors.New("camera: not connected")
	}

	seq := c.allocSeq()
	if err := c.sendRequest(seq, CmdIRCutGet, nil); err != nil {
		return 0, err
	}
	resp, err := c.waitResponse(ctx, seq, CmdIRCutGet, time.Now().Add(c.cfg.ConnectTimeout))
	if err != nil {
		return 0, err
	}
	plain, err := c.decrypt(resp)
	if err != nil {
		return 0, fmt.Errorf("camera: IRCutGet response undecryptable: %w", err)
	}
	mode, err := pb.FirstVarint(plain, 1)
	if err != nil {
		return 0, fmt.Errorf("camera: IRCutGet response carries no mode: %w", err)
	}
	return DayNightMode(mode), nil
}
