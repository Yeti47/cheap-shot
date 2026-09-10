package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Yeti47/cheap-shot/src/internal/camera"
	"github.com/Yeti47/cheap-shot/src/internal/pprpc"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := write(t, `{"cameras":[{"name":"cam1","host":"192.168.9.252","did":"D","scode":"S"}]}`)
	cfg, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Cameras[0]
	if c.Port != pprpc.Port || c.Prekey != pprpc.DefaultPrekey {
		t.Fatalf("defaults not applied: %+v", c)
	}
	if cfg.Listen != "127.0.0.1:8080" {
		t.Fatalf("listen = %q", cfg.Listen)
	}
	got, err := c.Password()
	if err != nil {
		t.Fatal(err)
	}
	if want := camera.LanPassword("D", "S", 0); got != want {
		t.Fatalf("password = %s, want %s", got, want)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	path := write(t, `{"cameras":[{"name":"cam-1","host":"10.0.0.1","did":"file","scode":"file"}]}`)
	t.Setenv("CHEAPSHOT_LISTEN", "0.0.0.0:9000")
	t.Setenv("CHEAPSHOT_CAM_1_DID", "envdid")
	t.Setenv("CHEAPSHOT_CAM_1_SCODE", "envscode")
	t.Setenv("CHEAPSHOT_CAM_1_HOST", "192.168.9.252")

	cfg, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Cameras[0]
	if cfg.Listen != "0.0.0.0:9000" || c.DID != "envdid" || c.SCode != "envscode" || c.Host != "192.168.9.252" {
		t.Fatalf("env overrides not applied: %+v %s", c, cfg.Listen)
	}
}

func TestCameraFromEnvOnly(t *testing.T) {
	t.Setenv("CHEAPSHOT_CAM1_HOST", "192.168.9.252")
	t.Setenv("CHEAPSHOT_CAM1_DID", "D")
	t.Setenv("CHEAPSHOT_CAM1_SCODE", "S")
	cfg, err := Load("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Cameras) != 1 || cfg.Cameras[0].Host != "192.168.9.252" {
		t.Fatalf("got %+v", cfg.Cameras)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]string{
		"no cameras":     `{"cameras":[]}`,
		"missing host":   `{"cameras":[{"name":"cam1","did":"D","scode":"S"}]}`,
		"missing did":    `{"cameras":[{"name":"cam1","host":"h"}]}`,
		"missing secret": `{"cameras":[{"name":"cam1","host":"h","did":"D"}]}`,
		"bad name":       `{"cameras":[{"name":"cam 1","host":"h","did":"D","scode":"S"}]}`,
		"duplicate":      `{"cameras":[{"name":"c","host":"h","did":"D","scode":"S"},{"name":"c","host":"h","did":"D","scode":"S"}]}`,
		"unknown field":  `{"camera":[]}`,
	}
	for name, body := range cases {
		if _, err := Load(write(t, body), nil); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestExplicitPasswordSkipsDerivation(t *testing.T) {
	path := write(t, `{"cameras":[{"name":"cam1","host":"h","lan_password":"$L0$abc"}]}`)
	cfg, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.Cameras[0].Password()
	if err != nil || got != "$L0$abc" {
		t.Fatalf("password = %s, %v", got, err)
	}
}

func TestUserCandidates(t *testing.T) {
	c := Camera{DID: "D"}
	if got := c.UserCandidates(); len(got) != 3 || got[0] != "" || got[1] != "D" {
		t.Fatalf("got %q", got)
	}
	c.User = "admin"
	if got := c.UserCandidates(); len(got) != 1 || got[0] != "admin" {
		t.Fatalf("got %q", got)
	}
}
