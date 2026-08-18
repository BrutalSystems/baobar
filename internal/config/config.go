// Package config resolves Baobar's settings from a TOML file and the environment.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// MinRecheck is a hard floor, not a preference. Every lookup-self lands in the
// OpenBao audit log; polling faster buries the decrypt events the log exists to
// surface. See the design doc's audit-log invariant before changing it.
const MinRecheck = 60 * time.Second

const (
	DefaultRecheck = 300 * time.Second
	DefaultWarn    = 30 * time.Minute
)

const (
	DefaultOIDCMount     = "oidc"
	DefaultUserpassMount = "userpass"
	DefaultCallbackPort  = 8250

	// DefaultOIDCPrompt makes the identity provider ask which account to use.
	// Login happens in the system browser, which reuses whatever provider
	// session is already open — so on a machine signed in to more than one work
	// account, one gets chosen silently, and a wrong choice fails later in the
	// code exchange as a missing claim rather than here. The cost on a
	// single-account machine is one click; set oidc_prompt = "" to opt out.
	DefaultOIDCPrompt = "select_account"
)

var (
	ErrNoAddr        = errors.New("no OpenBao address configured (set VAULT_ADDR or addr in config.toml)")
	ErrRecheckTooLow = fmt.Errorf("recheck below the %s minimum", MinRecheck)

	// ErrBadMount means an auth mount path is not a single safe path segment.
	ErrBadMount = errors.New("auth mount must be a single path segment of letters, digits, dashes or underscores")

	// Allowlists for URL components
	dnsHostRe  = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*(?::[0-9]{1,5})?$`)
	ipv6HostRe = regexp.MustCompile(`^\[[0-9A-Fa-f:.]+\](?::[0-9]{1,5})?$`)
	pathRe     = regexp.MustCompile(`^[A-Za-z0-9._~/-]*$`)
	mountRe    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// Config holds settings only. File paths come from DefaultPaths and are passed
// to the poller directly, so there is no half-populated Config to trip over.
type Config struct {
	Addr    string
	Recheck time.Duration
	Warn    time.Duration

	// Auth-flow settings. Mounts are path segments in OpenBao request URLs;
	// CallbackPort must match the OIDC role's allowed redirect URI.
	OIDCMount     string
	OIDCRole      string
	UserpassMount string
	CallbackPort  int
	Username      string
	// OIDCPrompt is sent to the identity provider as the OIDC `prompt`
	// parameter. Empty sends none.
	OIDCPrompt string
}

type fileConfig struct {
	Addr    string `toml:"addr,omitempty"`
	Recheck string `toml:"recheck,omitempty"`
	Warn    string `toml:"warn,omitempty"`

	OIDCMount     string `toml:"oidc_mount,omitempty"`
	OIDCRole      string `toml:"oidc_role,omitempty"`
	UserpassMount string `toml:"userpass_mount,omitempty"`
	CallbackPort  string `toml:"callback_port,omitempty"`
	Username      string `toml:"username,omitempty"`
	// A pointer, unlike every other field here: oidc_prompt has a non-empty
	// default, so an explicit "" is the only way to turn it off and must stay
	// distinguishable from the key being absent.
	OIDCPrompt *string `toml:"oidc_prompt,omitempty"`
}

// Load resolves settings with the file taking precedence over the environment.
// A missing or unreadable file is not an error; an unparseable one is.
func Load(path string, getenv func(string) string) (Config, error) {
	c := Config{Recheck: DefaultRecheck, Warn: DefaultWarn}

	var fc fileConfig
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, &fc); err != nil {
				return Config{}, fmt.Errorf("read %s: %w", path, err)
			}
		}
	}

	c.Addr = firstNonEmpty(fc.Addr, getenv("VAULT_ADDR"))
	if c.Addr == "" {
		return Config{}, ErrNoAddr
	}
	c.Addr = strings.TrimSuffix(c.Addr, "/")
	if err := ValidateAddr(c.Addr); err != nil {
		return Config{}, err
	}

	if s := firstNonEmpty(fc.Recheck, getenv("BAOBAR_RECHECK")); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return Config{}, fmt.Errorf("parse recheck %q: %w", s, err)
		}
		c.Recheck = d
	}
	if c.Recheck < MinRecheck {
		return Config{}, ErrRecheckTooLow
	}

	if s := firstNonEmpty(fc.Warn, getenv("BAOBAR_WARN")); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return Config{}, fmt.Errorf("parse warn %q: %w", s, err)
		}
		c.Warn = d
	}

	c.OIDCMount = firstNonEmpty(fc.OIDCMount, getenv("BAOBAR_OIDC_MOUNT"), DefaultOIDCMount)
	c.UserpassMount = firstNonEmpty(fc.UserpassMount, getenv("BAOBAR_USERPASS_MOUNT"), DefaultUserpassMount)
	for _, m := range []string{c.OIDCMount, c.UserpassMount} {
		if !mountRe.MatchString(m) {
			return Config{}, fmt.Errorf("%w: %q", ErrBadMount, m)
		}
	}

	c.OIDCRole = firstNonEmpty(fc.OIDCRole, getenv("BAOBAR_OIDC_ROLE"))
	c.Username = firstNonEmpty(fc.Username, getenv("BAOBAR_USERNAME"))

	// Not firstNonEmpty: that cannot tell "absent" from "deliberately empty",
	// and empty is the only way to switch the account chooser back off.
	c.OIDCPrompt = DefaultOIDCPrompt
	if fc.OIDCPrompt != nil {
		c.OIDCPrompt = *fc.OIDCPrompt
	} else if s := getenv("BAOBAR_OIDC_PROMPT"); s != "" {
		c.OIDCPrompt = s
	}

	c.CallbackPort = DefaultCallbackPort
	if s := firstNonEmpty(fc.CallbackPort, getenv("BAOBAR_CALLBACK_PORT")); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return Config{}, fmt.Errorf("parse callback_port %q: %w", s, err)
		}
		if n < 1 || n > 65535 {
			return Config{}, fmt.Errorf("callback_port %d is not a valid port", n)
		}
		c.CallbackPort = n
	}
	return c, nil
}

// ValidateAddr enforces a strict allowlist for URL components. This is the
// single validator for the address used to construct every OpenBao request
// URL — every authflow request and the "open web UI" tray link all build on
// this Addr, so a malformed or hostile value must be caught here, once,
// rather than trusted downstream.
func ValidateAddr(addr string) error {
	u, err := url.Parse(addr)
	if err != nil {
		return fmt.Errorf("parse addr %q: %w", addr, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("addr %q must be an http or https URL", addr)
	}
	if u.Host == "" {
		return fmt.Errorf("addr %q has no host", addr)
	}
	if u.User != nil {
		return fmt.Errorf("addr %q must not contain userinfo", addr)
	}
	if u.RawQuery != "" {
		return fmt.Errorf("addr %q must not contain a query string", addr)
	}
	if u.Fragment != "" {
		return fmt.Errorf("addr %q must not contain a fragment", addr)
	}

	// Validate host against allowlist (DNS name or IPv6 literal, each with optional port)
	if !dnsHostRe.MatchString(u.Host) && !ipv6HostRe.MatchString(u.Host) {
		return fmt.Errorf("addr %q host does not match allowlist", addr)
	}

	// Validate path against allowlist
	if u.Path != "" && !pathRe.MatchString(u.Path) {
		return fmt.Errorf("addr %q path contains disallowed characters", addr)
	}

	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// DefaultPaths returns the OS-appropriate config, token, and cache locations.
// The token path is deliberately the one the bao CLI and SOPS already use.
func DefaultPaths() (configPath, tokenPath, cachePath string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", "", err
	}
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", "", "", err
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", "", "", err
	}
	return filepath.Join(cfgDir, "baobar", "config.toml"),
		filepath.Join(home, ".vault-token"),
		filepath.Join(cacheDir, "baobar", "status.json"),
		nil
}

// SaveAddr persists addr to the config file so first-run setup does not require
// the user to hand-write TOML.
//
// It validates before writing: an address Load would later reject must never
// reach disk, or the next launch starts into a config it cannot use — the same
// dead end this is meant to remove.
//
// An existing file is read first and re-encoded with the new address, so
// settings someone put there by hand survive. The write goes through a
// temporary file in the same directory and is renamed into place, so an
// interrupted save cannot leave a half-written config.
func SaveAddr(path, addr string) error {
	addr = strings.TrimSuffix(strings.TrimSpace(addr), "/")
	if addr == "" {
		return ErrNoAddr
	}
	if err := ValidateAddr(addr); err != nil {
		return err
	}

	var fc fileConfig
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, &fc); err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
	}
	fc.Addr = addr

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if err := toml.NewEncoder(tmp).Encode(fc); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
