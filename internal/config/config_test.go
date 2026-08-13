package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func env(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

func TestLoadUsesEnvAddrWhenNoFile(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "absent.toml"),
		env(map[string]string{"VAULT_ADDR": "https://bao.example.com"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Addr != "https://bao.example.com" {
		t.Errorf("Addr = %q, want https://bao.example.com", c.Addr)
	}
	if c.Recheck != 300*time.Second {
		t.Errorf("Recheck = %v, want 5m", c.Recheck)
	}
	if c.Warn != 30*time.Minute {
		t.Errorf("Warn = %v, want 30m", c.Warn)
	}
}

// The spec fixes this precedence: an explicit config file beats the ambient env var.
func TestLoadFileBeatsEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("addr = \"https://from-file.example.com\"\nrecheck = \"10m\"\n"), 0o600)

	c, err := Load(p, env(map[string]string{"VAULT_ADDR": "https://from-env.example.com"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Addr != "https://from-file.example.com" {
		t.Errorf("Addr = %q, want the file value", c.Addr)
	}
	if c.Recheck != 10*time.Minute {
		t.Errorf("Recheck = %v, want 10m", c.Recheck)
	}
}

func TestLoadWithoutAddrIsAnError(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.toml"), env(nil))
	if !errors.Is(err, ErrNoAddr) {
		t.Fatalf("err = %v, want ErrNoAddr", err)
	}
}

// Guards the audit-log invariant: nobody gets to poll every 5 seconds.
func TestLoadRejectsRecheckBelowFloor(t *testing.T) {
	_, err := Load("", env(map[string]string{
		"VAULT_ADDR":     "https://bao.example.com",
		"BAOBAR_RECHECK": "5s",
	}))
	if !errors.Is(err, ErrRecheckTooLow) {
		t.Fatalf("err = %v, want ErrRecheckTooLow", err)
	}
}

// Addr is interpolated into a shell command by internal/login, so a non-URL
// must be rejected at the boundary rather than escaped later.
func TestLoadRejectsNonURLAddr(t *testing.T) {
	_, err := Load("", env(map[string]string{"VAULT_ADDR": "https://x.com\"; rm -rf ~"}))
	if err == nil {
		t.Fatal("expected an error for an addr containing a quote")
	}
}
