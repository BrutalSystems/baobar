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

func TestLoadAddrAllowlist(t *testing.T) {
	tests := []struct {
		name      string
		addr      string
		shouldErr bool
	}{
		// Accepted addresses
		{"simple hostname", "https://bao.example.com", false},
		{"localhost with port", "http://localhost:8200", false},
		{"IPv6 literal with port", "https://[::1]:8200", false},
		{"hostname with port and path", "https://bao.example.com:8200/prefix", false},

		// Rejected: shell metacharacters
		{"right angle bracket in host", "https://x.com>out", true},
		{"parentheses in host", "https://x.com(sub)", true},
		{"asterisk in host", "https://x.com*", true},
		{"quote and semicolon", "https://x.com\"; rm -rf ~", true},

		// Rejected: credentials
		{"userinfo in URL", "https://user:pw@bao.example.com", true},

		// Rejected: wrong scheme
		{"ftp scheme", "ftp://bao.example.com", true},

		// Rejected: query string
		{"query string", "https://bao.example.com/p?a=b", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load("", env(map[string]string{"VAULT_ADDR": tt.addr}))
			if tt.shouldErr && err == nil {
				t.Errorf("addr %q should have been rejected", tt.addr)
			}
			if !tt.shouldErr && err != nil {
				t.Errorf("addr %q should have been accepted: %v", tt.addr, err)
			}
		})
	}
}

func TestLoadAuthDefaults(t *testing.T) {
	c, err := Load("", env(map[string]string{"VAULT_ADDR": "https://bao.example.com"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.OIDCMount != "oidc" {
		t.Errorf("OIDCMount = %q, want oidc", c.OIDCMount)
	}
	if c.UserpassMount != "userpass" {
		t.Errorf("UserpassMount = %q, want userpass", c.UserpassMount)
	}
	if c.CallbackPort != 8250 {
		t.Errorf("CallbackPort = %d, want 8250", c.CallbackPort)
	}
	if c.OIDCRole != "" || c.Username != "" {
		t.Errorf("OIDCRole = %q, Username = %q, want both empty", c.OIDCRole, c.Username)
	}
}

func TestLoadAuthFromEnv(t *testing.T) {
	c, err := Load("", env(map[string]string{
		"VAULT_ADDR":            "https://bao.example.com",
		"BAOBAR_OIDC_MOUNT":     "jwt",
		"BAOBAR_OIDC_ROLE":      "developer",
		"BAOBAR_USERPASS_MOUNT": "userpass2",
		"BAOBAR_CALLBACK_PORT":  "8251",
		"BAOBAR_USERNAME":       "userpass-dev",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.OIDCMount != "jwt" || c.OIDCRole != "developer" ||
		c.UserpassMount != "userpass2" || c.CallbackPort != 8251 || c.Username != "userpass-dev" {
		t.Errorf("got %+v", c)
	}
}

// Mounts are interpolated into request paths, so anything that could escape a
// single path segment is rejected at the boundary.
func TestLoadRejectsUnsafeMounts(t *testing.T) {
	// An EMPTY mount is not invalid — it means "use the default" and is
	// resolved before validation ever sees it. Only malformed values are here.
	for _, bad := range []string{"oidc/../sys", "oidc/x", "oi dc", "oidc?x=1", "oidc#f", "."} {
		_, err := Load("", env(map[string]string{
			"VAULT_ADDR":        "https://bao.example.com",
			"BAOBAR_OIDC_MOUNT": bad,
		}))
		if !errors.Is(err, ErrBadMount) {
			t.Errorf("mount %q: err = %v, want ErrBadMount", bad, err)
		}
	}
}

func TestLoadRejectsBadCallbackPort(t *testing.T) {
	for _, bad := range []string{"0", "70000", "-1", "http"} {
		_, err := Load("", env(map[string]string{
			"VAULT_ADDR":           "https://bao.example.com",
			"BAOBAR_CALLBACK_PORT": bad,
		}))
		if err == nil {
			t.Errorf("port %q: expected an error", bad)
		}
	}
}
