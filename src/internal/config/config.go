// Package config loads the bridge's JSON configuration and applies the
// environment overrides that let a container run without a mounted file.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Yeti47/cheap-shot/src/internal/camera"
	"github.com/Yeti47/cheap-shot/src/internal/pprpc"
)

var safeName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Camera is one camera the bridge should connect to.
type Camera struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port,omitempty"`

	// DID is the device id (factory PRODUCT_KEY, e.g. "PP00A9XXXXXXXXXX";
	// it also drives the setup-AP SSID). LSLat is the device's encrypted
	// config secret (factory record's DEVICE_SECRET, hex-decoded to a base64
	// string, e.g. "AAAA...=="). The LanAuth secret is
	// Deckey(DID, LSLat) -- the AES-decrypted form -- NOT PRODUCT_SECRET.
	// Both are device-local; never commit them.
	DID   string `json:"did,omitempty"`
	LSLat string `json:"lslat,omitempty"`

	// SCode optionally supplies the already-decrypted auth secret directly
	// (the Deckey(DID, LSLat) result), bypassing LSLat.
	SCode string `json:"scode,omitempty"`

	// User is the LanAuth username. Leave empty to try the built-in
	// candidates (empty, the did, "admin").
	User      string `json:"user,omitempty"`
	AuthIndex int    `json:"auth_index,omitempty"`
	// Rotation is clockwise and must be a quarter-turn amount.
	Rotation int `json:"rotation,omitempty"`

	// Prekey overrides the control-plane key material recovered from the
	// stock firmware; LanPassword overrides the derivation entirely.
	Prekey      string `json:"prekey,omitempty"`
	LanPassword string `json:"lan_password,omitempty"`

	V4L2Device string `json:"v4l2_device,omitempty"`
}

// Config is the whole bridge configuration.
type Config struct {
	Listen  string   `json:"listen"`
	Cameras []Camera `json:"cameras"`
}

// Password returns the LanAuth password for this camera. Precedence: an
// explicit LanPassword override; else the auth secret from SCode; else the
// secret recovered from LSLat via Deckey. The secret is then folded into
// "$L<idx>$"+MD5(did-secret-idx).
func (c Camera) Password() (string, error) {
	if c.LanPassword != "" {
		return c.LanPassword, nil
	}
	scode := c.SCode
	if scode == "" {
		var err error
		if scode, err = camera.Deckey(c.DID, c.LSLat); err != nil {
			return "", fmt.Errorf("config: camera %q: deriving auth secret from lslat: %w", c.Name, err)
		}
	}
	return camera.LanPassword(c.DID, scode, c.AuthIndex), nil
}

// UserCandidates lists the usernames to try when none was configured.
func (c Camera) UserCandidates() []string {
	if c.User != "" {
		return []string{c.User}
	}
	return []string{"", c.DID, "admin"}
}

// Load reads path (when non-empty), applies environment overrides and
// validates the result.
func Load(path string, log *slog.Logger) (*Config, error) {
	cfg := &Config{Listen: "127.0.0.1:8080"}
	fileLoaded := false
	if path != "" {
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(cfg); err != nil {
				return nil, fmt.Errorf("config: %s: %w", path, err)
			}
			fileLoaded = true
		case errors.Is(err, os.ErrNotExist):
			// No file at the (possibly default) path: fall back to a purely
			// environment-driven config, which is the intended Docker/.env
			// path. validate() still errors if the environment supplies no
			// camera either.
			if log != nil {
				log.Info("config file not found; using environment only", "path", path)
			}
		default:
			return nil, fmt.Errorf("config: %w", err)
		}
	}
	applyEnv(cfg)
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if fileLoaded {
		warnIfInGitTree(path, cfg, log)
	}
	return cfg, nil
}

// applyEnv lets CHEAPSHOT_LISTEN and CHEAPSHOT_<CAM>_{HOST,DID,LSLAT,SCODE,USER,ROTATION,V4L2_DEVICE}
// override the file, so secrets need never be written to disk.
func applyEnv(cfg *Config) {
	if v := os.Getenv("CHEAPSHOT_LISTEN"); v != "" {
		cfg.Listen = v
	}
	// A camera can be introduced entirely from the environment.
	if len(cfg.Cameras) == 0 {
		if host := os.Getenv("CHEAPSHOT_CAM1_HOST"); host != "" {
			cfg.Cameras = append(cfg.Cameras, Camera{Name: "cam1", Host: host})
		}
	}
	for i := range cfg.Cameras {
		c := &cfg.Cameras[i]
		prefix := "CHEAPSHOT_" + envKey(c.Name) + "_"
		setEnv(&c.Host, prefix+"HOST")
		setEnv(&c.DID, prefix+"DID")
		setEnv(&c.LSLat, prefix+"LSLAT")
		setEnv(&c.SCode, prefix+"SCODE")
		setEnv(&c.User, prefix+"USER")
		setEnv(&c.Prekey, prefix+"PREKEY")
		setEnv(&c.LanPassword, prefix+"LAN_PASSWORD")
		setEnv(&c.V4L2Device, prefix+"V4L2_DEVICE")
		if v := os.Getenv(prefix + "ROTATION"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				c.Rotation = n
			}
		}
		if v := os.Getenv(prefix + "PORT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				c.Port = n
			}
		}
	}
}

func setEnv(dst *string, key string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func envKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func (cfg *Config) validate() error {
	if cfg.Listen == "" {
		return fmt.Errorf("config: listen must not be empty")
	}
	if len(cfg.Cameras) == 0 {
		return fmt.Errorf("config: at least one camera must be configured")
	}
	seen := map[string]bool{}
	for i := range cfg.Cameras {
		c := &cfg.Cameras[i]
		if !safeName.MatchString(c.Name) {
			return fmt.Errorf("config: invalid camera name %q (use A-Z, a-z, 0-9, _ or -)", c.Name)
		}
		if seen[c.Name] {
			return fmt.Errorf("config: duplicate camera name %q", c.Name)
		}
		seen[c.Name] = true
		if c.Host == "" {
			return fmt.Errorf("config: camera %q needs a host", c.Name)
		}
		if c.Port == 0 {
			c.Port = pprpc.Port
		}
		if c.Rotation != 0 && c.Rotation != 90 && c.Rotation != 180 && c.Rotation != 270 {
			return fmt.Errorf("config: camera %q rotation must be 0, 90, 180, or 270 degrees", c.Name)
		}
		if c.Prekey == "" {
			c.Prekey = pprpc.DefaultPrekey
		}
		if c.LanPassword == "" && c.DID == "" {
			return fmt.Errorf("config: camera %q needs a did (PRODUCT_KEY), or an explicit lan_password", c.Name)
		}
		if c.LanPassword == "" && c.LSLat == "" && c.SCode == "" {
			return fmt.Errorf("config: camera %q needs lslat (DEVICE_SECRET) or scode, "+
				"or an explicit lan_password", c.Name)
		}
	}
	return nil
}

// warnIfInGitTree points out a config holding device secrets that sits inside
// a git working tree, where it is one `git add -f` away from being published.
func warnIfInGitTree(path string, cfg *Config, log *slog.Logger) {
	if log == nil {
		return
	}
	hasSecret := false
	for _, c := range cfg.Cameras {
		if c.DID != "" || c.LSLat != "" || c.SCode != "" || c.LanPassword != "" {
			hasSecret = true
		}
	}
	if !hasSecret {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	for dir := filepath.Dir(abs); ; {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			log.Warn("config with device secrets lives inside a git working tree; "+
				"make sure it is gitignored", "config", abs, "repo", dir)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}
