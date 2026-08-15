# Baobar M1 (Indicator) Implementation Plan

> **Historical record.** This is the implementation plan as written before the work began.
> It is kept because the reasoning is useful, but its code listings are a *snapshot of the
> intent*, not of the shipped source — several were corrected during implementation and
> review. Always trust the code and the tests over this document. See `docs/NEXT.md` for
> current state.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a cross-platform tray app that shows OpenBao login state and a live token
countdown, with menu actions to log out, refresh, and launch a login in a terminal.

**Architecture:** A pure-Go core (`internal/bao`) reads `~/.vault-token`, calls
`GET /v1/auth/token/lookup-self` over HTTP, and caches the absolute expiry so the countdown
is computed locally between infrequent server checks. A thin `internal/tray` layer renders
that state into a menu via `fyne.io/systray`. Every decision that can be wrong lives in the
core and is unit-tested with a fake clock and a fake server; the tray layer holds no logic.

**Tech Stack:** Go 1.26.3, `fyne.io/systray`, `github.com/BurntSushi/toml`,
`github.com/gen2brain/beeep`, stdlib `net/http` + `net/http/httptest`.

**Spec:** `docs/superpowers/specs/2026-08-13-baobar-design.md` — read it first. This plan
implements milestone **M1** only. M2 (in-app userpass+TOTP) and M3 (in-app OIDC) are out of
scope; M1 shells out to a terminal for login, exactly as the shell prototype does.

## Global Constraints

Every task's requirements implicitly include this section.

- **Go 1.26.3**, pinned via a project `.tool-versions` (asdf). Without it, `go` fails with
  "No version is set for command go" in this workspace.
- **Module path:** `github.com/BrutalSystems/baobar`. *Assumption* — the GitHub org was never
  confirmed. If it differs, run `go mod edit -module github.com/<org>/baobar` before the
  first push; nothing else in the plan depends on it.
- **The systray code below was written against the `getlantern/systray` API** (`Run`,
  `SetTitle`, `SetIcon`, `SetTooltip`, `AddMenuItem`, `ClickedCh`), which is what was
  actually verified. `fyne.io/systray` is a fork of it and is expected to match, but that
  was *not* confirmed. If the first build in Task 10 fails on a signature, this is why —
  either adapt the call or switch the import to `github.com/getlantern/systray`.
- **Never invoke the `bao` CLI to determine status.** Status comes from the HTTP API. The CLI
  is optional and only appears in the M1 login shell-out. A machine without the CLI must
  still show a correct indicator.
- **THIS REPO IS PUBLIC.** Nothing you commit may contain a real hostname, username,
  policy name, cluster or namespace name, token, or absolute path from a private machine.
  Docs, tests, comments, and fixtures use placeholders only: `bao.example.com`,
  `userpass-dev`, policies `admin` / `deploy`, and repo-relative paths. Environment-specific
  values live in `docs/local/`, which is git-ignored — never reference its contents from a
  committed file except by that path. If a task seems to need a real value, use a
  placeholder and note it in your report; do not improvise a realistic-looking one.
- **Never hardcode any server address in code** — not even a placeholder — outside
  documentation examples and test fixtures. `VAULT_ADDR` is user-supplied; with none
  configured, say so plainly.
- **Never log, print, or write a token value** anywhere: not in errors, not in debug
  output, not in the cache. Errors refer to "the token", never its contents.
- **`~/.vault-token` (`%USERPROFILE%\.vault-token`) is the token location.** Shared with the
  `bao` CLI and SOPS. Do not relocate it or invent a private store.
- **Minimum recheck interval is 60s; the default is 300s.** This is the audit-log invariant
  from the spec — every `lookup-self` lands in the audit log, and 30s polling would add
  ~2,900 audited requests per user per day. Task 1 enforces the floor in code so a future
  session cannot lower it by accident.
- **The cache stores no token material** — only expiry, display name, and policy names.
  Deleting it must be harmless.
- **The four states — `SignedIn`, `Expiring`, `SignedOut`, `Degraded` — stay distinct.**
  A network failure renders as `Degraded`, never as `SignedOut`. This is the single most
  likely regression.

### Two spec amendments discovered while planning

Both were verified against the systray source and are folded into the tasks below:

1. **`SetTitle` renders no text on Windows.** The Windows tray has an icon and a tooltip,
   full stop. So the countdown reaches Windows users through `SetTooltip` plus a
   color-coded icon, and every state must be distinguishable by icon alone. This is why
   Task 8 generates four icons rather than deferring artwork to a later milestone.
2. **Linux cannot be cross-compiled from your Mac.** Linux systray needs `CGO_ENABLED=1`
   plus `gcc`, `libgtk-3-dev`, and `libayatana-appindicator3-dev`. Windows *can* be
   cross-compiled (`CGO_ENABLED=0`, pure-Go syscalls) and needs
   `-ldflags "-H=windowsgui"` to suppress a console window. Task 10 records this in the
   README instead of pretending one machine builds all three.

---

## File Structure

```
baobar/
  .tool-versions                     golang 1.26.3
  .gitignore
  go.mod / go.sum
  cmd/baobar/main.go                 wiring only: config -> poller -> tray loop
  internal/config/config.go          Addr/Recheck/Warn resolution, paths, validation
  internal/config/config_test.go
  internal/bao/state.go              State, Status, Classify        (pure)
  internal/bao/token.go              ReadToken / WriteToken
  internal/bao/client.go             LookupSelf, RevokeSelf
  internal/bao/cache.go              Cache load/save/delete/freshness
  internal/bao/poller.go             orchestration: token -> cache -> API -> Status
  internal/bao/*_test.go
  internal/notify/tracker.go         threshold crossing, once per token
  internal/notify/notify.go          beeep wrapper
  internal/tray/format.go            Human, Label, Tooltip          (pure)
  internal/tray/icons.go             go:embed of the four PNGs
  internal/tray/tray.go              systray wiring                 (no logic)
  internal/tray/assets/*.png
  internal/login/command.go          per-GOOS terminal command builder (pure)
  tools/genicons/main.go             one-shot PNG generator
```

The boundary that matters: everything above `tray.go` and `main.go` is testable without a
GUI. `tray.go` and `main.go` are the only files verified by hand.

---

### Task 1: Repo skeleton and config

**Files:**
- Create: `.tool-versions`, `.gitignore`, `go.mod`, `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.Config{Addr string, Recheck, Warn time.Duration}` and
  `config.Load(path string, getenv func(string) string) (Config, error)`, plus
  `config.DefaultPaths() (configPath, tokenPath, cachePath string, err error)`.
  Sentinel errors: `config.ErrNoAddr`, `config.ErrRecheckTooLow`.

`Config` deliberately holds no file paths. An earlier draft had `TokenPath`/`CachePath`
fields that `Load` never populated — a trap for anyone calling `Load` without also calling
`DefaultPaths`. Paths come from `DefaultPaths` and are passed straight to the `Poller`.

- [ ] **Step 1: Initialize the repo and pin the toolchain**

The repo is already a git repository with the docs committed, and `.gitignore` already
excludes `docs/local/` and `.superpowers/`. Do not re-run `git init`, and do not remove
existing `.gitignore` entries — append to them.

```bash
printf 'golang 1.26.3\n' > .tool-versions
printf 'baobar\nbaobar.exe\ndist/\n' >> .gitignore
go mod init github.com/BrutalSystems/baobar
go get github.com/BurntSushi/toml
go version   # must print go1.26.3
```

- [ ] **Step 2: Write the failing test**

Create `internal/config/config_test.go`:

```go
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
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/config/ -run TestLoad -v`
Expected: FAIL — `undefined: Load`, `undefined: ErrNoAddr`.

- [ ] **Step 4: Write the implementation**

Create `internal/config/config.go`:

```go
// Package config resolves Baobar's settings from a TOML file and the environment.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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

var (
	ErrNoAddr        = errors.New("no OpenBao address configured (set VAULT_ADDR or addr in config.toml)")
	ErrRecheckTooLow = fmt.Errorf("recheck below the %s minimum", MinRecheck)
)

// Config holds settings only. File paths come from DefaultPaths and are passed
// to the poller directly, so there is no half-populated Config to trip over.
type Config struct {
	Addr    string
	Recheck time.Duration
	Warn    time.Duration
}

type fileConfig struct {
	Addr    string `toml:"addr"`
	Recheck string `toml:"recheck"`
	Warn    string `toml:"warn"`
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
	if err := validateAddr(c.Addr); err != nil {
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
	return c, nil
}

// validateAddr keeps shell metacharacters out of the string that internal/login
// interpolates into a terminal command.
func validateAddr(addr string) error {
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
	if strings.ContainsAny(addr, "\"'`$;&|\n\r ") {
		return fmt.Errorf("addr %q contains characters that are unsafe to shell out with", addr)
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
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS, 5 tests.

- [ ] **Step 6: Commit**

```bash
git add .tool-versions .gitignore go.mod go.sum internal/config/
git commit -m "feat(config): resolve addr, recheck, and paths with an audit-safe floor"
```

---

### Task 2: State classification

**Files:**
- Create: `internal/bao/state.go`
- Test: `internal/bao/state_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `bao.State` (`StateSignedOut`, `StateSignedIn`, `StateExpiring`,
  `StateDegraded`), `bao.Status{State, Remaining, ExpiresAt, Name, Policies, NeverExpires}`,
  and `bao.Classify(remaining time.Duration, reachable, expiryKnown bool, warn time.Duration) State`.

- [ ] **Step 1: Write the failing test**

Create `internal/bao/state_test.go`:

```go
package bao

import (
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	const warn = 30 * time.Minute

	tests := []struct {
		name        string
		remaining   time.Duration
		reachable   bool
		expiryKnown bool
		want        State
	}{
		{"plenty of time", 6 * time.Hour, true, true, StateSignedIn},
		{"inside the warning window", 22 * time.Minute, true, true, StateExpiring},
		{"exactly at the threshold", warn, true, true, StateExpiring},
		{"just past the threshold", warn + time.Second, true, true, StateSignedIn},
		{"expired", -time.Second, true, true, StateSignedOut},
		{"expiring exactly now", 0, true, true, StateSignedOut},

		// A network failure must never read as logged out: the token is still
		// valid and nagging the user into a pointless re-login is the bug.
		{"unreachable with time left", 6 * time.Hour, false, true, StateDegraded},
		{"unreachable inside warning window", 22 * time.Minute, false, true, StateDegraded},

		// Expiry is absolute, so a known-expired token is out regardless of
		// whether we can reach the server to confirm it.
		{"unreachable and expired", -time.Second, false, true, StateSignedOut},

		// Token file present, never successfully looked up, server down.
		{"unreachable with no known expiry", 0, false, false, StateDegraded},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.remaining, tc.reachable, tc.expiryKnown, warn)
			if got != tc.want {
				t.Errorf("Classify(%v, reachable=%v, known=%v) = %v, want %v",
					tc.remaining, tc.reachable, tc.expiryKnown, got, tc.want)
			}
		})
	}
}

func TestStateString(t *testing.T) {
	for state, want := range map[State]string{
		StateSignedOut: "signed-out",
		StateSignedIn:  "signed-in",
		StateExpiring:  "expiring",
		StateDegraded:  "degraded",
	} {
		if got := state.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", state, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/bao/ -run TestClassify -v`
Expected: FAIL — `undefined: Classify`.

- [ ] **Step 3: Write the implementation**

Create `internal/bao/state.go`:

```go
// Package bao holds Baobar's token state machine, API client, and cache.
// It knows nothing about menus and never shells out to the bao CLI.
package bao

import "time"

type State int

const (
	StateSignedOut State = iota
	StateSignedIn
	StateExpiring
	StateDegraded
)

func (s State) String() string {
	switch s {
	case StateSignedIn:
		return "signed-in"
	case StateExpiring:
		return "expiring"
	case StateDegraded:
		return "degraded"
	default:
		return "signed-out"
	}
}

// Status is the full picture the tray renders.
type Status struct {
	State     State
	Remaining time.Duration // clamped at zero; meaningless when NeverExpires
	// ExpiresAt is the absolute expiry, zero when unknown. The tray uses it to
	// identify the current token when tracking notification thresholds — do not
	// remove it and reconstruct it as now+Remaining at the call site.
	ExpiresAt    time.Time
	Name         string   // display_name, e.g. "userpass-dev"
	Policies     []string // "default" already filtered out
	NeverExpires bool     // root and other non-expiring tokens
}

// Classify decides the state. reachable is false when the server could not be
// reached and the caller is falling back to cached data; expiryKnown is false
// when no cached expiry exists at all.
func Classify(remaining time.Duration, reachable, expiryKnown bool, warn time.Duration) State {
	if !reachable {
		if expiryKnown && remaining <= 0 {
			return StateSignedOut
		}
		return StateDegraded
	}
	if remaining <= 0 {
		return StateSignedOut
	}
	if remaining <= warn {
		return StateExpiring
	}
	return StateSignedIn
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/bao/ -v`
Expected: PASS, 11 subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/bao/state.go internal/bao/state_test.go
git commit -m "feat(bao): classify token state, keeping degraded distinct from signed-out"
```

---

### Task 3: Token file

**Files:**
- Create: `internal/bao/token.go`
- Test: `internal/bao/token_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `bao.ReadToken(path string) (string, error)`, `bao.WriteToken(path, token string) error`,
  sentinel `bao.ErrNoToken`.

- [ ] **Step 1: Write the failing test**

Create `internal/bao/token_test.go`:

```go
package bao

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadTokenMissingFile(t *testing.T) {
	_, err := ReadToken(filepath.Join(t.TempDir(), "absent"))
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestReadTokenTrimsWhitespace(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".vault-token")
	os.WriteFile(p, []byte("  hvs.CAESIabc123\n"), 0o600)

	got, err := ReadToken(p)
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if got != "hvs.CAESIabc123" {
		t.Errorf("token = %q, want the trimmed value", got)
	}
}

func TestReadTokenTreatsBlankFileAsAbsent(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".vault-token")
	os.WriteFile(p, []byte("\n  \n"), 0o600)

	if _, err := ReadToken(p); !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestWriteTokenIsPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".vault-token")
	if err := WriteToken(p, "hvs.new"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}
	got, err := ReadToken(p)
	if err != nil || got != "hvs.new" {
		t.Fatalf("round trip = %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(p)
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("mode = %o, want 600", perm)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/bao/ -run TestReadToken -v`
Expected: FAIL — `undefined: ReadToken`.

- [ ] **Step 3: Write the implementation**

Create `internal/bao/token.go`:

```go
package bao

import (
	"errors"
	"os"
	"strings"
)

// ErrNoToken means there is no usable token on disk — the user is signed out.
var ErrNoToken = errors.New("no token file")

// ReadToken reads the token the bao CLI and SOPS also use. Do not relocate this
// file: interoperability with those tools is the point.
func ReadToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoToken
	}
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", ErrNoToken
	}
	return token, nil
}

// WriteToken stores a token for the CLI and SOPS to pick up. Unused in M1;
// M2 and M3 call it after an in-app login.
func WriteToken(path, token string) error {
	return os.WriteFile(path, []byte(token), 0o600)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/bao/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bao/token.go internal/bao/token_test.go
git commit -m "feat(bao): read and write the shared ~/.vault-token"
```

---

### Task 4: API client

**Files:**
- Create: `internal/bao/client.go`
- Test: `internal/bao/client_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `bao.Info{ExpiresAt time.Time, Name string, Policies []string, NeverExpires bool}`,
  `bao.NewClient(addr string) *Client`,
  `(*Client).LookupSelf(ctx context.Context, token string, now time.Time) (Info, error)`,
  `(*Client).RevokeSelf(ctx context.Context, token string) error`,
  sentinel `bao.ErrForbidden`.

- [ ] **Step 1: Write the failing test**

Create `internal/bao/client_test.go`:

```go
package bao

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLookupSelfParsesAbsoluteExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Vault-Token"); got != "hvs.abc" {
			t.Errorf("X-Vault-Token = %q, want hvs.abc", got)
		}
		if r.URL.Path != "/v1/auth/token/lookup-self" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write([]byte(`{"data":{
			"expire_time":"2026-08-14T02:19:00.123456789Z",
			"display_name":"userpass-dev",
			"policies":["default","admin","deploy"],
			"ttl":22740}}`))
	}))
	defer srv.Close()

	now := time.Date(2026, 8, 13, 20, 0, 0, 0, time.UTC)
	info, err := NewClient(srv.URL).LookupSelf(context.Background(), "hvs.abc", now)
	if err != nil {
		t.Fatalf("LookupSelf: %v", err)
	}

	want := time.Date(2026, 8, 14, 2, 19, 0, 123456789, time.UTC)
	if !info.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", info.ExpiresAt, want)
	}
	if info.Name != "userpass-dev" {
		t.Errorf("Name = %q", info.Name)
	}
	// "default" is noise in a menu; every token has it.
	if len(info.Policies) != 2 || info.Policies[0] != "admin" || info.Policies[1] != "deploy" {
		t.Errorf("Policies = %v, want [admin deploy]", info.Policies)
	}
	if info.NeverExpires {
		t.Error("NeverExpires = true, want false")
	}
}

// Some tokens report only a relative ttl.
func TestLookupSelfFallsBackToTTL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"expire_time":"","ttl":3600,"display_name":"x","policies":["default"]}}`))
	}))
	defer srv.Close()

	now := time.Date(2026, 8, 13, 20, 0, 0, 0, time.UTC)
	info, err := NewClient(srv.URL).LookupSelf(context.Background(), "t", now)
	if err != nil {
		t.Fatalf("LookupSelf: %v", err)
	}
	if !info.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Errorf("ExpiresAt = %v, want now+1h", info.ExpiresAt)
	}
}

// Root tokens have no expiry at all; showing them as expired would be wrong.
func TestLookupSelfDetectsNonExpiringToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"expire_time":"","ttl":0,"display_name":"root","policies":["root"]}}`))
	}))
	defer srv.Close()

	info, err := NewClient(srv.URL).LookupSelf(context.Background(), "t", time.Now())
	if err != nil {
		t.Fatalf("LookupSelf: %v", err)
	}
	if !info.NeverExpires {
		t.Error("NeverExpires = false, want true")
	}
}

func TestLookupSelfForbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"errors":["permission denied"]}`))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL).LookupSelf(context.Background(), "stale", time.Now())
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

// A dead server must produce a plain error, NOT ErrForbidden — the poller
// distinguishes the two to decide between Degraded and SignedOut.
func TestLookupSelfNetworkErrorIsNotForbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close()

	_, err := NewClient(addr).LookupSelf(context.Background(), "t", time.Now())
	if err == nil {
		t.Fatal("expected an error from a closed server")
	}
	if errors.Is(err, ErrForbidden) {
		t.Fatal("network failure must not be reported as ErrForbidden")
	}
}

func TestRevokeSelf(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost || r.URL.Path != "/v1/auth/token/revoke-self" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := NewClient(srv.URL).RevokeSelf(context.Background(), "hvs.abc"); err != nil {
		t.Fatalf("RevokeSelf: %v", err)
	}
	if !called {
		t.Error("server was never called")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/bao/ -run 'TestLookupSelf|TestRevokeSelf' -v`
Expected: FAIL — `undefined: NewClient`.

- [ ] **Step 3: Write the implementation**

Create `internal/bao/client.go`:

```go
package bao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ErrForbidden means the server rejected the token: it is revoked or expired.
// It is deliberately distinct from a transport error — the poller treats the
// former as signed out and the latter as degraded.
var ErrForbidden = errors.New("token rejected by server")

// Info is one successful lookup-self.
type Info struct {
	ExpiresAt    time.Time
	Name         string
	Policies     []string
	NeverExpires bool
}

type Client struct {
	Addr string
	HTTP *http.Client
}

func NewClient(addr string) *Client {
	return &Client{
		Addr: strings.TrimSuffix(addr, "/"),
		HTTP: &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *Client) LookupSelf(ctx context.Context, token string, now time.Time) (Info, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v1/auth/token/lookup-self", token)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()

	var body struct {
		Data struct {
			ExpireTime  string   `json:"expire_time"`
			TTL         int64    `json:"ttl"`
			DisplayName string   `json:"display_name"`
			Policies    []string `json:"policies"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Info{}, fmt.Errorf("decode lookup-self: %w", err)
	}

	info := Info{Name: body.Data.DisplayName}
	for _, p := range body.Data.Policies {
		if p != "default" {
			info.Policies = append(info.Policies, p)
		}
	}

	switch {
	case body.Data.ExpireTime != "":
		t, err := time.Parse(time.RFC3339Nano, body.Data.ExpireTime)
		if err != nil {
			return Info{}, fmt.Errorf("parse expire_time %q: %w", body.Data.ExpireTime, err)
		}
		info.ExpiresAt = t.UTC()
	case body.Data.TTL > 0:
		info.ExpiresAt = now.Add(time.Duration(body.Data.TTL) * time.Second).UTC()
	default:
		// No expire_time and no ttl: a root or otherwise non-expiring token.
		info.NeverExpires = true
	}
	return info, nil
}

func (c *Client) RevokeSelf(ctx context.Context, token string) error {
	resp, err := c.do(ctx, http.MethodPost, "/v1/auth/token/revoke-self", token)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *Client) do(ctx context.Context, method, path, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.Addr+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err // transport failure: caller renders this as Degraded
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return resp, nil
	case http.StatusForbidden, http.StatusUnauthorized:
		resp.Body.Close()
		return nil, ErrForbidden
	default:
		resp.Body.Close()
		return nil, fmt.Errorf("%s %s: unexpected status %d", method, path, resp.StatusCode)
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/bao/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bao/client.go internal/bao/client_test.go
git commit -m "feat(bao): lookup-self and revoke-self over HTTP, no CLI dependency"
```

---

### Task 5: Expiry cache

**Files:**
- Create: `internal/bao/cache.go`
- Test: `internal/bao/cache_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `bao.TokenStamp{ModTime, Size int64}`, `bao.StampToken(path string) TokenStamp`,
  `bao.Cache{CheckedAt, ExpiresAt int64, NeverExpires bool, Name string, Policies []string, Token TokenStamp}`,
  `bao.LoadCache(path string) (Cache, bool)`, `bao.SaveCache(path string, c Cache) error`,
  `bao.DeleteCache(path string) error`,
  `(Cache).Fresh(now time.Time, recheck time.Duration, stamp TokenStamp) bool`.

`TokenStamp` exists because a cache keyed only by *when* it was written cannot tell that
the token underneath it changed. Log in again from a terminal while the cache is still
inside its recheck window and, without the stamp, Baobar shows the *previous* token's
expiry for up to five minutes. The stamp is the token file's mtime and size — metadata, not
token material, so the "cache holds no secrets" rule still holds.

- [ ] **Step 1: Write the failing test**

Create `internal/bao/cache_test.go`:

```go
package bao

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCacheRoundTripCreatesParentDir(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "status.json")
	in := Cache{CheckedAt: 1000, ExpiresAt: 2000, Name: "userpass-dev", Policies: []string{"admin"}}

	if err := SaveCache(p, in); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	got, ok := LoadCache(p)
	if !ok {
		t.Fatal("LoadCache: ok = false")
	}
	if got.ExpiresAt != 2000 || got.Name != "userpass-dev" || len(got.Policies) != 1 {
		t.Errorf("round trip = %+v", got)
	}
}

// The cache must never contain token material. Deleting it is always safe.
func TestCacheHoldsNoTokenMaterial(t *testing.T) {
	p := filepath.Join(t.TempDir(), "status.json")
	SaveCache(p, Cache{CheckedAt: 1000, ExpiresAt: 2000, Name: "userpass-dev"})

	b, _ := os.ReadFile(p)
	for _, field := range []string{"token", "hvs.", "client_token"} {
		if strings.Contains(string(b), field) {
			t.Errorf("cache file contains %q: %s", field, b)
		}
	}
}

func TestLoadCacheMissingOrCorrupt(t *testing.T) {
	dir := t.TempDir()

	if _, ok := LoadCache(filepath.Join(dir, "absent.json")); ok {
		t.Error("ok = true for a missing file")
	}

	corrupt := filepath.Join(dir, "corrupt.json")
	os.WriteFile(corrupt, []byte("{not json"), 0o600)
	if _, ok := LoadCache(corrupt); ok {
		t.Error("ok = true for a corrupt file")
	}
}

func TestCacheFresh(t *testing.T) {
	now := time.Unix(10_000, 0)
	const recheck = 300 * time.Second
	stamp := TokenStamp{ModTime: 500, Size: 95}

	tests := []struct {
		name  string
		cache Cache
		want  bool
	}{
		{"checked recently, not expired",
			Cache{CheckedAt: 9_900, ExpiresAt: 20_000, Token: stamp}, true},
		{"checked too long ago",
			Cache{CheckedAt: 9_000, ExpiresAt: 20_000, Token: stamp}, false},
		{"exactly at the recheck boundary",
			Cache{CheckedAt: 9_700, ExpiresAt: 20_000, Token: stamp}, false},
		// Recent check but the token has since expired: must re-verify, not
		// serve a stale "signed in".
		{"recent but expired",
			Cache{CheckedAt: 9_900, ExpiresAt: 9_950, Token: stamp}, false},
		{"non-expiring token",
			Cache{CheckedAt: 9_900, ExpiresAt: 0, NeverExpires: true, Token: stamp}, true},
		// The user logged in again from a terminal: same time window, different
		// token. Serving the old expiry here is the bug this field prevents.
		{"token file changed underneath us",
			Cache{CheckedAt: 9_900, ExpiresAt: 20_000, Token: TokenStamp{ModTime: 400, Size: 95}}, false},
		{"token file changed size only",
			Cache{CheckedAt: 9_900, ExpiresAt: 20_000, Token: TokenStamp{ModTime: 500, Size: 12}}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cache.Fresh(now, recheck, stamp); got != tc.want {
				t.Errorf("Fresh() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStampToken(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".vault-token")
	os.WriteFile(p, []byte("hvs.abc"), 0o600)

	first := StampToken(p)
	if first.Size != 7 || first.ModTime == 0 {
		t.Fatalf("StampToken = %+v, want size 7 and a real mtime", first)
	}
	if same := StampToken(p); same != first {
		t.Errorf("StampToken is not stable: %+v then %+v", first, same)
	}

	// A rewrite with different content must change the stamp.
	os.WriteFile(p, []byte("hvs.a-much-longer-token"), 0o600)
	if changed := StampToken(p); changed == first {
		t.Error("StampToken did not change after the token was rewritten")
	}

	// A missing file stamps as zero, which never equals a real stamp.
	if missing := StampToken(filepath.Join(t.TempDir(), "absent")); missing != (TokenStamp{}) {
		t.Errorf("StampToken(missing) = %+v, want zero", missing)
	}
}

func TestDeleteCacheIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "status.json")
	if err := DeleteCache(p); err != nil {
		t.Fatalf("DeleteCache on a missing file: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/bao/ -run TestCache -v`
Expected: FAIL — `undefined: SaveCache`.

- [ ] **Step 3: Write the implementation**

Create `internal/bao/cache.go`:

```go
package bao

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// TokenStamp identifies which token a cache entry describes, using only file
// metadata — never the token itself.
type TokenStamp struct {
	ModTime int64 `json:"mod_time"`
	Size    int64 `json:"size"`
}

// StampToken returns the stamp for a token file, or the zero stamp if it cannot
// be read. The zero value never compares equal to a real stamp, so an unreadable
// token file always invalidates the cache.
func StampToken(path string) TokenStamp {
	fi, err := os.Stat(path)
	if err != nil {
		return TokenStamp{}
	}
	return TokenStamp{ModTime: fi.ModTime().UnixNano(), Size: fi.Size()}
}

// Cache is what lets the countdown run locally between server checks. It holds
// no token material — only an expiry, a display name, policy names, and the
// token file's mtime and size — so deleting it is always safe and merely forces
// a fresh lookup.
type Cache struct {
	CheckedAt    int64      `json:"checked_at"`
	ExpiresAt    int64      `json:"expires_at"`
	NeverExpires bool       `json:"never_expires"`
	Name         string     `json:"name"`
	Policies     []string   `json:"policies"`
	Token        TokenStamp `json:"token_stamp"`
}

// LoadCache reports ok=false for a missing or unreadable cache. A corrupt cache
// is not an error worth surfacing: the caller just re-checks the server.
func LoadCache(path string) (Cache, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Cache{}, false
	}
	var c Cache
	if err := json.Unmarshal(b, &c); err != nil {
		return Cache{}, false
	}
	return c, true
}

func SaveCache(path string, c Cache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func DeleteCache(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Fresh reports whether the cache can be trusted without calling the server.
// stamp is the token file's current stamp: if it differs from the one recorded,
// the user logged in again and this entry describes a token that is gone.
func (c Cache) Fresh(now time.Time, recheck time.Duration, stamp TokenStamp) bool {
	if c.Token != stamp {
		return false
	}
	if now.Unix()-c.CheckedAt >= int64(recheck.Seconds()) {
		return false
	}
	if c.NeverExpires {
		return true
	}
	return c.ExpiresAt > now.Unix()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/bao/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bao/cache.go internal/bao/cache_test.go
git commit -m "feat(bao): cache absolute expiry so the countdown runs locally"
```

---

### Task 6: Poller

This is where the audit-log invariant is actually enforced and where `Degraded` is
actually produced. It is the highest-value test in the project.

**Files:**
- Create: `internal/bao/poller.go`
- Test: `internal/bao/poller_test.go`

**Interfaces:**
- Consumes: `ReadToken`, `Classify`, `Cache`, `Info`, `ErrForbidden`, `ErrNoToken`.
- Produces: `bao.Lookuper` interface (one method: `LookupSelf(ctx, token string, now time.Time) (Info, error)`),
  `bao.Poller{Client Lookuper, TokenPath, CachePath string, Recheck, Warn time.Duration, Now func() time.Time}`,
  and `(*Poller).Status(ctx context.Context) Status`.

- [ ] **Step 1: Write the failing test**

Create `internal/bao/poller_test.go`:

```go
package bao

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeLookuper struct {
	info  Info
	err   error
	calls int
}

func (f *fakeLookuper) LookupSelf(_ context.Context, _ string, _ time.Time) (Info, error) {
	f.calls++
	return f.info, f.err
}

// newPoller wires a poller onto a temp dir with a frozen clock.
func newPoller(t *testing.T, l Lookuper, now time.Time) (*Poller, string, string) {
	t.Helper()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, ".vault-token")
	cachePath := filepath.Join(dir, "status.json")
	return &Poller{
		Client:    l,
		TokenPath: tokenPath,
		CachePath: cachePath,
		Recheck:   300 * time.Second,
		Warn:      30 * time.Minute,
		Now:       func() time.Time { return now },
	}, tokenPath, cachePath
}

// writeToken writes a token file and returns the stamp a cache entry must carry
// to be considered fresh for it.
func writeToken(t *testing.T, path, token string) TokenStamp {
	t.Helper()
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	return StampToken(path)
}

func TestStatusWithoutTokenFileNeverCallsTheServer(t *testing.T) {
	l := &fakeLookuper{}
	p, _, _ := newPoller(t, l, time.Unix(10_000, 0))

	got := p.Status(context.Background())
	if got.State != StateSignedOut {
		t.Errorf("State = %v, want signed-out", got.State)
	}
	if l.calls != 0 {
		t.Errorf("server called %d times with no token on disk", l.calls)
	}
}

// The audit-log invariant, enforced: a fresh cache means no request.
func TestStatusUsesFreshCacheWithoutCallingTheServer(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{}
	p, tokenPath, cachePath := newPoller(t, l, now)
	stamp := writeToken(t, tokenPath, "hvs.abc")
	SaveCache(cachePath, Cache{
		CheckedAt: 9_900, ExpiresAt: 10_000 + int64(6*time.Hour/time.Second),
		Name: "userpass-dev", Policies: []string{"admin"}, Token: stamp,
	})

	got := p.Status(context.Background())
	if l.calls != 0 {
		t.Errorf("server called %d times despite a fresh cache", l.calls)
	}
	if got.State != StateSignedIn {
		t.Errorf("State = %v, want signed-in", got.State)
	}
	if got.Remaining != 6*time.Hour {
		t.Errorf("Remaining = %v, want 6h", got.Remaining)
	}
	if got.ExpiresAt.Unix() != 10_000+int64(6*time.Hour/time.Second) {
		t.Errorf("ExpiresAt = %v, want the cached absolute expiry", got.ExpiresAt)
	}
	if got.Name != "userpass-dev" {
		t.Errorf("Name = %q, want the cached name", got.Name)
	}
}

// Logging in again from a terminal replaces the token file. The cache is still
// inside its recheck window, but it describes a token that no longer exists, so
// it must be re-checked rather than served.
func TestStatusRechecksWhenTokenFileChanges(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{info: Info{ExpiresAt: now.Add(8 * time.Hour), Name: "userpass-dev"}}
	p, tokenPath, cachePath := newPoller(t, l, now)

	writeToken(t, tokenPath, "hvs.old")
	SaveCache(cachePath, Cache{
		CheckedAt: 9_900, ExpiresAt: 10_000 + int64(90*time.Minute/time.Second),
		Name: "userpass-dev", Token: StampToken(tokenPath),
	})

	// The user re-authenticates in a terminal: same path, different content.
	writeToken(t, tokenPath, "hvs.freshly-issued-and-longer")

	got := p.Status(context.Background())
	if l.calls != 1 {
		t.Fatalf("server called %d times, want 1 — a new token must force a lookup", l.calls)
	}
	if got.Remaining != 8*time.Hour {
		t.Errorf("Remaining = %v, want the new token's 8h, not the cached 90m", got.Remaining)
	}
}

func TestStatusRefreshesStaleCache(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{info: Info{
		ExpiresAt: now.Add(2 * time.Hour), Name: "userpass-dev", Policies: []string{"admin"},
	}}
	p, tokenPath, cachePath := newPoller(t, l, now)
	stamp := writeToken(t, tokenPath, "hvs.abc")
	SaveCache(cachePath, Cache{CheckedAt: 1, ExpiresAt: 99_999, Name: "old", Token: stamp})

	got := p.Status(context.Background())
	if l.calls != 1 {
		t.Errorf("server called %d times, want 1", l.calls)
	}
	if got.Remaining != 2*time.Hour {
		t.Errorf("Remaining = %v, want 2h", got.Remaining)
	}

	c, ok := LoadCache(cachePath)
	if !ok || c.CheckedAt != now.Unix() || c.Name != "userpass-dev" {
		t.Errorf("cache not refreshed: %+v", c)
	}
}

func TestStatusExpiringWithinWarningWindow(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{info: Info{ExpiresAt: now.Add(22 * time.Minute), Name: "userpass-dev"}}
	p, tokenPath, _ := newPoller(t, l, now)
	writeToken(t, tokenPath, "hvs.abc")

	if got := p.Status(context.Background()); got.State != StateExpiring {
		t.Errorf("State = %v, want expiring", got.State)
	}
}

// A rejected token is genuinely signed out, and the stale cache must go.
func TestStatusForbiddenClearsCache(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{err: ErrForbidden}
	p, tokenPath, cachePath := newPoller(t, l, now)
	stamp := writeToken(t, tokenPath, "hvs.revoked")
	SaveCache(cachePath, Cache{CheckedAt: 1, ExpiresAt: 99_999, Name: "old", Token: stamp})

	got := p.Status(context.Background())
	if got.State != StateSignedOut {
		t.Errorf("State = %v, want signed-out", got.State)
	}
	if _, ok := LoadCache(cachePath); ok {
		t.Error("cache survived a 403")
	}
}

// THE regression to guard: an unreachable server is not a logout.
func TestStatusNetworkErrorIsDegradedNotSignedOut(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{err: errors.New("dial tcp: connection refused")}
	p, tokenPath, cachePath := newPoller(t, l, now)
	stamp := writeToken(t, tokenPath, "hvs.abc")
	SaveCache(cachePath, Cache{
		CheckedAt: 1, ExpiresAt: 10_000 + int64(6*time.Hour/time.Second),
		Name: "userpass-dev", Token: stamp,
	})

	got := p.Status(context.Background())
	if got.State != StateDegraded {
		t.Fatalf("State = %v, want degraded", got.State)
	}
	if got.Remaining != 6*time.Hour {
		t.Errorf("Remaining = %v, want the cached 6h countdown to keep running", got.Remaining)
	}
	if _, ok := LoadCache(cachePath); !ok {
		t.Error("cache was cleared by a mere network failure")
	}
}

func TestStatusNetworkErrorWithNoCacheIsDegraded(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{err: errors.New("dial tcp: connection refused")}
	p, tokenPath, _ := newPoller(t, l, now)
	writeToken(t, tokenPath, "hvs.abc")

	got := p.Status(context.Background())
	if got.State != StateDegraded {
		t.Errorf("State = %v, want degraded", got.State)
	}
	if got.Remaining != 0 {
		t.Errorf("Remaining = %v, want 0 (unknown)", got.Remaining)
	}
}

// A cached token whose expiry has passed is out, even offline.
func TestStatusExpiredCacheOfflineIsSignedOut(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{err: errors.New("dial tcp: connection refused")}
	p, tokenPath, cachePath := newPoller(t, l, now)
	stamp := writeToken(t, tokenPath, "hvs.abc")
	SaveCache(cachePath, Cache{CheckedAt: 1, ExpiresAt: 9_000, Name: "userpass-dev", Token: stamp})

	if got := p.Status(context.Background()); got.State != StateSignedOut {
		t.Errorf("State = %v, want signed-out", got.State)
	}
}

func TestStatusNonExpiringToken(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{info: Info{NeverExpires: true, Name: "root", Policies: []string{"root"}}}
	p, tokenPath, _ := newPoller(t, l, now)
	writeToken(t, tokenPath, "hvs.root")

	got := p.Status(context.Background())
	if got.State != StateSignedIn || !got.NeverExpires {
		t.Errorf("got %+v, want signed-in and NeverExpires", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/bao/ -run TestStatus -v`
Expected: FAIL — `undefined: Poller`.

- [ ] **Step 3: Write the implementation**

Create `internal/bao/poller.go`:

```go
package bao

import (
	"context"
	"errors"
	"time"
)

// Lookuper is the slice of Client the poller needs, so tests can fake it.
type Lookuper interface {
	LookupSelf(ctx context.Context, token string, now time.Time) (Info, error)
}

// Poller turns the token file, the cache, and the server into a single Status.
//
// It calls the server at most once per Recheck window. Do not "fix" a stale-
// looking countdown by shortening Recheck — the countdown is recomputed locally
// on every call and is never stale. See the design doc's audit-log invariant.
//
// Status blocks on the network when the cache is cold, so callers must run it
// off any UI goroutine (see internal/tray).
type Poller struct {
	Client    Lookuper
	TokenPath string
	CachePath string
	Recheck   time.Duration
	Warn      time.Duration
	Now       func() time.Time
}

func (p *Poller) Status(ctx context.Context) Status {
	now := p.Now()

	token, err := ReadToken(p.TokenPath)
	if err != nil {
		_ = DeleteCache(p.CachePath)
		return Status{State: StateSignedOut}
	}

	stamp := StampToken(p.TokenPath)
	if c, ok := LoadCache(p.CachePath); ok && c.Fresh(now, p.Recheck, stamp) {
		return p.statusFrom(c, now, true)
	}

	info, err := p.Client.LookupSelf(ctx, token, now)
	switch {
	case errors.Is(err, ErrForbidden):
		// The server actively rejected this token: genuinely signed out.
		_ = DeleteCache(p.CachePath)
		return Status{State: StateSignedOut}

	case err != nil:
		// Transport failure. The token is probably still fine — keep counting
		// down from cache and show degraded rather than crying logout.
		if c, ok := LoadCache(p.CachePath); ok {
			return p.statusFrom(c, now, false)
		}
		return Status{State: StateDegraded}
	}

	c := Cache{
		CheckedAt:    now.Unix(),
		ExpiresAt:    info.ExpiresAt.Unix(),
		NeverExpires: info.NeverExpires,
		Name:         info.Name,
		Policies:     info.Policies,
		Token:        stamp,
	}
	_ = SaveCache(p.CachePath, c)
	return p.statusFrom(c, now, true)
}

func (p *Poller) statusFrom(c Cache, now time.Time, reachable bool) Status {
	s := Status{Name: c.Name, Policies: c.Policies, NeverExpires: c.NeverExpires}
	if c.NeverExpires {
		s.State = StateSignedIn
		if !reachable {
			s.State = StateDegraded
		}
		return s
	}

	expiresAt := time.Unix(c.ExpiresAt, 0)
	remaining := expiresAt.Sub(now)
	s.State = Classify(remaining, reachable, true, p.Warn)
	if remaining < 0 {
		remaining = 0
	}
	s.Remaining = remaining
	s.ExpiresAt = expiresAt
	return s
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/bao/ -v`
Expected: PASS, all nine poller tests plus the earlier ones.

- [ ] **Step 5: Commit**

```bash
git add internal/bao/poller.go internal/bao/poller_test.go
git commit -m "feat(bao): poll on a cache-first schedule, degrading instead of logging out"
```

---

### Task 7: Notification thresholds

**Files:**
- Create: `internal/notify/tracker.go`, `internal/notify/notify.go`
- Test: `internal/notify/tracker_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `notify.NewTracker(thresholds ...time.Duration) *Tracker`,
  `(*Tracker).Crossed(tokenKey int64, remaining time.Duration) []time.Duration`,
  `notify.Send(title, message string) error`.

- [ ] **Step 1: Write the failing test**

Create `internal/notify/tracker_test.go`:

```go
package notify

import (
	"testing"
	"time"
)

func newTestTracker() *Tracker {
	return NewTracker(30*time.Minute, 5*time.Minute)
}

func TestTrackerFiresOncePerThreshold(t *testing.T) {
	tr := newTestTracker()
	const token = int64(2000)

	if got := tr.Crossed(token, 6*time.Hour); len(got) != 0 {
		t.Errorf("Crossed at 6h = %v, want none", got)
	}
	got := tr.Crossed(token, 29*time.Minute)
	if len(got) != 1 || got[0] != 30*time.Minute {
		t.Fatalf("Crossed at 29m = %v, want [30m]", got)
	}
	// Ticking again inside the same window must stay silent.
	if got := tr.Crossed(token, 28*time.Minute); len(got) != 0 {
		t.Errorf("Crossed at 28m = %v, want none", got)
	}
	got = tr.Crossed(token, 4*time.Minute)
	if len(got) != 1 || got[0] != 5*time.Minute {
		t.Fatalf("Crossed at 4m = %v, want [5m]", got)
	}
	if got := tr.Crossed(token, 3*time.Minute); len(got) != 0 {
		t.Errorf("Crossed at 3m = %v, want none", got)
	}
}

// Jumping straight past both thresholds reports both, in descending order.
func TestTrackerReportsMultipleCrossingsAtOnce(t *testing.T) {
	tr := newTestTracker()
	got := tr.Crossed(2000, 2*time.Minute)
	if len(got) != 2 || got[0] != 30*time.Minute || got[1] != 5*time.Minute {
		t.Fatalf("Crossed = %v, want [30m 5m]", got)
	}
}

// A new token (new expiry) is a new countdown: warnings must arm again.
func TestTrackerResetsOnNewToken(t *testing.T) {
	tr := newTestTracker()
	tr.Crossed(2000, 29*time.Minute)

	got := tr.Crossed(9999, 29*time.Minute)
	if len(got) != 1 || got[0] != 30*time.Minute {
		t.Fatalf("Crossed after re-login = %v, want [30m]", got)
	}
}

// Already expired is the tray's problem, not the notifier's.
func TestTrackerSilentWhenExpired(t *testing.T) {
	tr := newTestTracker()
	if got := tr.Crossed(2000, 0); len(got) != 0 {
		t.Errorf("Crossed at 0 = %v, want none", got)
	}
	if got := tr.Crossed(2000, -time.Minute); len(got) != 0 {
		t.Errorf("Crossed at -1m = %v, want none", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/notify/ -v`
Expected: FAIL — `undefined: NewTracker`.

- [ ] **Step 3: Write the implementation**

Create `internal/notify/tracker.go`:

```go
// Package notify handles expiry warnings: deciding when one is due, and showing it.
package notify

import (
	"sort"
	"time"
)

// Tracker fires each threshold at most once per token. tokenKey identifies the
// current token (its expiry, in Unix seconds) so a re-login re-arms every warning.
type Tracker struct {
	thresholds []time.Duration
	key        int64
	fired      map[time.Duration]bool
}

func NewTracker(thresholds ...time.Duration) *Tracker {
	sorted := append([]time.Duration(nil), thresholds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] > sorted[j] })
	return &Tracker{thresholds: sorted, fired: map[time.Duration]bool{}}
}

// Crossed returns the thresholds newly crossed by this tick, longest first.
func (t *Tracker) Crossed(tokenKey int64, remaining time.Duration) []time.Duration {
	if tokenKey != t.key {
		t.key = tokenKey
		t.fired = map[time.Duration]bool{}
	}
	if remaining <= 0 {
		return nil
	}

	var due []time.Duration
	for _, th := range t.thresholds {
		if remaining <= th && !t.fired[th] {
			t.fired[th] = true
			due = append(due, th)
		}
	}
	return due
}
```

Create `internal/notify/notify.go`:

```go
package notify

import "github.com/gen2brain/beeep"

// Send shows a desktop notification. Failures are the caller's to ignore:
// a missing notification daemon must never take the tray down.
func Send(title, message string) error {
	return beeep.Notify(title, message, "")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go get github.com/gen2brain/beeep
go test ./internal/notify/ -v
```
Expected: PASS, 4 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/notify/ go.mod go.sum
git commit -m "feat(notify): warn once per threshold, re-arming on a new token"
```

---

### Task 8: Tray formatting and icons

Windows shows no text in the tray, so every state must be legible from the icon alone.
That is why this task generates four icons rather than deferring artwork.

**Files:**
- Create: `internal/tray/format.go`, `internal/tray/icons.go`, `tools/genicons/main.go`,
  `internal/tray/assets/{signedin,expiring,signedout,degraded}.png`
- Test: `internal/tray/format_test.go`

**Interfaces:**
- Consumes: `bao.Status`, `bao.State`.
- Produces: `tray.Human(d time.Duration) string`, `tray.Label(s bao.Status) string`,
  `tray.Tooltip(s bao.Status) string`, `tray.Icon(s bao.State) []byte`,
  `tray.MenuLines(s bao.Status) (who, policies, expires string)`.

`MenuLines` lives here rather than in `tray.go` on purpose: it is the last piece of
decision-making the menu needs, so keeping it in the tested, GUI-free file leaves `tray.go`
as pure wiring. An empty return value means "hide that row".

- [ ] **Step 1: Write the failing test**

Create `internal/tray/format_test.go`:

```go
package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/BrutalSystems/baobar/internal/bao"
)

func TestHuman(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{6*time.Hour + 19*time.Minute, "6h19m"},
		{22 * time.Minute, "22m"},
		{90 * time.Minute, "1h30m"},
		{time.Hour + 5*time.Minute, "1h05m"}, // zero-padded past the hour
		{0, "0m"},
		{-time.Minute, "0m"},
		{59 * time.Second, "0m"},
	}
	for _, tc := range tests {
		if got := Human(tc.in); got != tc.want {
			t.Errorf("Human(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLabel(t *testing.T) {
	tests := []struct {
		name string
		in   bao.Status
		want string
	}{
		{"signed in", bao.Status{State: bao.StateSignedIn, Remaining: 6*time.Hour + 19*time.Minute}, "🔓 6h19m"},
		{"expiring", bao.Status{State: bao.StateExpiring, Remaining: 22 * time.Minute}, "🟠 22m"},
		{"signed out", bao.Status{State: bao.StateSignedOut}, "🔒 login"},
		{"degraded with a cached countdown", bao.Status{State: bao.StateDegraded, Remaining: 6 * time.Hour}, "🌐 6h00m"},
		{"degraded with nothing cached", bao.Status{State: bao.StateDegraded}, "🌐 ?"},
		{"non-expiring", bao.Status{State: bao.StateSignedIn, NeverExpires: true}, "🔓 ∞"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Label(tc.in); got != tc.want {
				t.Errorf("Label() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The tooltip carries the whole story on Windows, where Label is never shown.
func TestTooltipCarriesFullStateForWindows(t *testing.T) {
	got := Tooltip(bao.Status{
		State: bao.StateSignedIn, Remaining: 6 * time.Hour,
		Name: "userpass-dev", Policies: []string{"admin", "deploy"},
	})
	for _, want := range []string{"OpenBao", "userpass-dev", "6h00m"} {
		if !strings.Contains(got, want) {
			t.Errorf("Tooltip() = %q, missing %q", got, want)
		}
	}

	if got := Tooltip(bao.Status{State: bao.StateSignedOut}); !strings.Contains(got, "Not signed in") {
		t.Errorf("Tooltip() = %q, want it to say not signed in", got)
	}
}

func TestMenuLines(t *testing.T) {
	who, policies, expires := MenuLines(bao.Status{
		State: bao.StateSignedIn, Remaining: 6 * time.Hour,
		Name: "userpass-dev", Policies: []string{"admin", "deploy"},
	})
	if who != "Signed in as userpass-dev" {
		t.Errorf("who = %q", who)
	}
	if policies != "Policies: admin, deploy" {
		t.Errorf("policies = %q", policies)
	}
	if expires != "Expires in 6h00m" {
		t.Errorf("expires = %q", expires)
	}

	// An empty string means "hide this row".
	who, policies, expires = MenuLines(bao.Status{State: bao.StateSignedOut})
	if who != "Not signed in" || policies != "" || expires != "" {
		t.Errorf("signed out = %q / %q / %q, want the last two empty", who, policies, expires)
	}

	who, _, expires = MenuLines(bao.Status{State: bao.StateDegraded, Remaining: time.Hour})
	if who != "Server unreachable" {
		t.Errorf("degraded who = %q", who)
	}
	if expires != "Showing the last known session" {
		t.Errorf("degraded expires = %q", expires)
	}

	if _, _, expires = MenuLines(bao.Status{State: bao.StateSignedIn, NeverExpires: true}); expires != "Never expires" {
		t.Errorf("non-expiring expires = %q", expires)
	}

	if _, policies, _ = MenuLines(bao.Status{State: bao.StateSignedIn, Name: "x"}); policies != "Policies: none" {
		t.Errorf("no policies = %q", policies)
	}
}

// Every state needs its own icon: on Windows it is the only signal.
func TestEveryStateHasADistinctIcon(t *testing.T) {
	seen := map[string]bao.State{}
	for _, s := range []bao.State{bao.StateSignedIn, bao.StateExpiring, bao.StateSignedOut, bao.StateDegraded} {
		b := Icon(s)
		if len(b) == 0 {
			t.Fatalf("Icon(%v) is empty", s)
		}
		if prev, dup := seen[string(b)]; dup {
			t.Errorf("Icon(%v) is identical to Icon(%v)", s, prev)
		}
		seen[string(b)] = s
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tray/ -v`
Expected: FAIL — `undefined: Human`.

- [ ] **Step 3: Generate the icons**

Create `tools/genicons/main.go`:

```go
// Command genicons writes Baobar's placeholder tray icons: a filled circle per
// state. Run with `go run ./tools/genicons`. Replace with real artwork before
// release (see the design doc's open question 4).
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
)

const size = 22

func main() {
	states := map[string]color.RGBA{
		"signedin": {0x3b, 0xa5, 0x5d, 0xff}, // green
		"expiring": {0xe0, 0x8b, 0x1f, 0xff}, // amber
		"signedout": {0xc0, 0x39, 0x2b, 0xff}, // red
		"degraded": {0x8a, 0x8a, 0x8a, 0xff}, // grey
	}
	dir := filepath.Join("internal", "tray", "assets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}

	for name, c := range states {
		img := image.NewRGBA(image.Rect(0, 0, size, size))
		const r = size/2 - 2
		cx, cy := size/2, size/2
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				dx, dy := x-cx, y-cy
				if dx*dx+dy*dy <= r*r {
					img.Set(x, y, c)
				}
			}
		}

		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			log.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			log.Fatal(err)
		}
		f.Close()
	}
	log.Printf("wrote 4 icons to %s", dir)
}
```

Run from the repo root:

```bash
go run ./tools/genicons
ls internal/tray/assets/    # signedin.png expiring.png signedout.png degraded.png
```

- [ ] **Step 4: Write the implementation**

Create `internal/tray/icons.go`:

```go
package tray

import (
	_ "embed"

	"github.com/BrutalSystems/baobar/internal/bao"
)

// Icons are embedded so the binary has no runtime asset dependency. Regenerate
// with `go run ./tools/genicons`.
var (
	//go:embed assets/signedin.png
	iconSignedIn []byte
	//go:embed assets/expiring.png
	iconExpiring []byte
	//go:embed assets/signedout.png
	iconSignedOut []byte
	//go:embed assets/degraded.png
	iconDegraded []byte
)

// Icon returns the tray image for a state. On Windows this is the only signal
// the user gets — there is no text label in the tray — so the four must differ
// at a glance, not merely differ.
func Icon(s bao.State) []byte {
	switch s {
	case bao.StateSignedIn:
		return iconSignedIn
	case bao.StateExpiring:
		return iconExpiring
	case bao.StateDegraded:
		return iconDegraded
	default:
		return iconSignedOut
	}
}
```

Create `internal/tray/format.go`:

```go
// Package tray renders bao.Status into a system tray icon and menu. It holds no
// logic of its own: everything decidable lives in internal/bao.
package tray

import (
	"fmt"
	"strings"
	"time"

	"github.com/BrutalSystems/baobar/internal/bao"
)

// Human formats a countdown as 6h19m or 22m, never negative.
func Human(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d / time.Hour)
	m := int(d/time.Minute) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// Label is the menu bar text. macOS and Linux only — Windows shows no title.
func Label(s bao.Status) string {
	switch s.State {
	case bao.StateSignedIn:
		if s.NeverExpires {
			return "🔓 ∞"
		}
		return "🔓 " + Human(s.Remaining)
	case bao.StateExpiring:
		return "🟠 " + Human(s.Remaining)
	case bao.StateDegraded:
		if s.NeverExpires {
			return "🌐 ∞"
		}
		if s.Remaining <= 0 {
			return "🌐 ?"
		}
		return "🌐 " + Human(s.Remaining)
	default:
		return "🔒 login"
	}
}

// Tooltip is the whole status in one line. This is the primary readout on
// Windows, so it must stand alone without the label.
func Tooltip(s bao.Status) string {
	switch s.State {
	case bao.StateSignedIn, bao.StateExpiring:
		return fmt.Sprintf("OpenBao: signed in as %s, %s left%s",
			s.Name, Human(s.Remaining), policySuffix(s.Policies))
	case bao.StateDegraded:
		if s.Remaining > 0 {
			return fmt.Sprintf("OpenBao: server unreachable — %s left on the cached session", Human(s.Remaining))
		}
		return "OpenBao: server unreachable, session unknown"
	default:
		return "OpenBao: Not signed in"
	}
}

func policySuffix(policies []string) string {
	if len(policies) == 0 {
		return ""
	}
	return " [" + strings.Join(policies, ", ") + "]"
}

// MenuLines returns the three informational rows of the menu. An empty string
// means the row should be hidden. This is the menu's last piece of judgement,
// kept here so tray.go stays pure wiring.
func MenuLines(s bao.Status) (who, policies, expires string) {
	switch s.State {
	case bao.StateSignedIn, bao.StateExpiring:
		who = "Signed in as " + s.Name
		policies = "Policies: " + policyList(s.Policies)
		if s.NeverExpires {
			expires = "Never expires"
		} else {
			expires = "Expires in " + Human(s.Remaining)
		}
	case bao.StateDegraded:
		who = "Server unreachable"
		expires = "Showing the last known session"
	default:
		who = "Not signed in"
	}
	return who, policies, expires
}

func policyList(policies []string) string {
	if len(policies) == 0 {
		return "none"
	}
	return strings.Join(policies, ", ")
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/tray/ -v`
Expected: PASS, 5 tests (7 Human cases, 6 Label cases, plus tooltip, menu lines, and icons).

- [ ] **Step 6: Commit**

```bash
git add internal/tray/ tools/genicons/
git commit -m "feat(tray): format label, tooltip, and per-state icons for Windows parity"
```

---

### Task 9: Login and logout commands

**Files:**
- Create: `internal/login/command.go`
- Test: `internal/login/command_test.go`

**Interfaces:**
- Consumes: nothing (`addr` is pre-validated by `config.Load`).
- Produces: `login.Command(goos, addr, method string) (name string, args []string, err error)`,
  `login.Launch(addr, method string) error`, and `login.CLIAvailable() bool`. Methods are
  `login.MethodUserpass` and `login.MethodOIDC`.

`CLIAvailable` exists because M1's login shells out to `bao login`, and Decision 2 of the
spec says Windows users are the least likely to have that CLI. Without the check, a Windows
user gets a correct indicator and a **Login** button that opens a console reading
`bao: command not found`. The tray uses it to disable the login items and say so instead.

- [ ] **Step 1: Write the failing test**

Create `internal/login/command_test.go`:

```go
package login

import (
	"errors"
	"strings"
	"testing"
)

func TestCommandPerPlatform(t *testing.T) {
	tests := []struct {
		goos     string
		wantName string
		wantIn   string
	}{
		{"darwin", "osascript", "do script"},
		{"windows", "cmd", "bao login"},
		{"linux", "x-terminal-emulator", "bao login"},
	}

	for _, tc := range tests {
		t.Run(tc.goos, func(t *testing.T) {
			name, args, err := Command(tc.goos, "https://bao.example.com", MethodUserpass)
			if err != nil {
				t.Fatalf("Command: %v", err)
			}
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, tc.wantIn) {
				t.Errorf("args = %q, want it to contain %q", joined, tc.wantIn)
			}
			if !strings.Contains(joined, "https://bao.example.com") {
				t.Errorf("args = %q, missing the address", joined)
			}
			if !strings.Contains(joined, "-method=userpass") {
				t.Errorf("args = %q, missing the method", joined)
			}
		})
	}
}

func TestCommandOIDCMethod(t *testing.T) {
	_, args, err := Command("darwin", "https://bao.example.com", MethodOIDC)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if !strings.Contains(strings.Join(args, " "), "-method=oidc") {
		t.Errorf("args = %q, want -method=oidc", args)
	}
}

func TestCommandRejectsUnknownMethod(t *testing.T) {
	if _, _, err := Command("darwin", "https://bao.example.com", "telepathy"); err == nil {
		t.Fatal("expected an error for an unknown method")
	}
}

func TestCommandRejectsUnknownOS(t *testing.T) {
	if _, _, err := Command("plan9", "https://bao.example.com", MethodUserpass); err == nil {
		t.Fatal("expected an error for an unsupported OS")
	}
}

// config.Load already rejects these, but this is the string that reaches a
// shell, so it refuses defensively too.
func TestCommandRejectsUnsafeAddr(t *testing.T) {
	if _, _, err := Command("darwin", "https://x.com\"; rm -rf ~", MethodUserpass); err == nil {
		t.Fatal("expected an error for an addr containing shell metacharacters")
	}
}

func TestCLIAvailable(t *testing.T) {
	found := func(string) (string, error) { return "/opt/homebrew/bin/bao", nil }
	missing := func(string) (string, error) { return "", errors.New("executable file not found in $PATH") }

	if !cliAvailable(found) {
		t.Error("cliAvailable = false when bao is on PATH")
	}
	if cliAvailable(missing) {
		t.Error("cliAvailable = true when bao is absent")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/login/ -v`
Expected: FAIL — `undefined: Command`.

- [ ] **Step 3: Write the implementation**

Create `internal/login/command.go`:

```go
// Package login launches a terminal running `bao login`. This is M1's stopgap:
// M2 and M3 replace it with in-app auth, at which point the bao CLI stops being
// required for anything.
package login

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

const (
	MethodUserpass = "userpass"
	MethodOIDC     = "oidc"
)

// Command builds the terminal invocation for a platform. goos is a parameter
// rather than a build tag so every branch is testable from one machine.
func Command(goos, addr, method string) (string, []string, error) {
	switch method {
	case MethodUserpass, MethodOIDC:
	default:
		return "", nil, fmt.Errorf("unknown login method %q", method)
	}
	if strings.ContainsAny(addr, "\"'`$;&|\n\r ") {
		return "", nil, fmt.Errorf("refusing to shell out with addr %q", addr)
	}

	baoCmd := fmt.Sprintf("bao login -method=%s", method)

	switch goos {
	case "darwin":
		script := fmt.Sprintf("export VAULT_ADDR=%s; %s", addr, baoCmd)
		return "osascript", []string{
			"-e", fmt.Sprintf("tell application \"Terminal\" to do script \"%s\"", script),
			"-e", "tell application \"Terminal\" to activate",
		}, nil

	case "windows":
		// `start` opens a new console; PowerShell keeps it open for the prompt.
		ps := fmt.Sprintf("$env:VAULT_ADDR='%s'; %s", addr, baoCmd)
		return "cmd", []string{"/c", "start", "powershell", "-NoExit", "-Command", ps}, nil

	case "linux":
		script := fmt.Sprintf("VAULT_ADDR=%s %s; exec $SHELL", addr, baoCmd)
		return "x-terminal-emulator", []string{"-e", "sh", "-c", script}, nil

	default:
		return "", nil, fmt.Errorf("unsupported OS %q", goos)
	}
}

// Launch starts the login terminal and returns without waiting for it.
func Launch(addr, method string) error {
	name, args, err := Command(runtime.GOOS, addr, method)
	if err != nil {
		return err
	}
	return exec.Command(name, args...).Start()
}

// CLIAvailable reports whether `bao` is on PATH. M1 cannot log in without it,
// and on Windows it is frequently absent — the tray checks this so the login
// items can explain themselves instead of opening a console that fails.
func CLIAvailable() bool { return cliAvailable(exec.LookPath) }

func cliAvailable(look func(string) (string, error)) bool {
	_, err := look("bao")
	return err == nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/login/ -v`
Expected: PASS, 5 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/login/
git commit -m "feat(login): build per-platform terminal login commands"
```

---

### Task 10: Tray wiring, main, and the build matrix

The only task whose deliverable is verified by hand. Keep `tray.go` free of decisions —
if you find yourself writing an `if` about token state here, it belongs in `internal/bao`.

**Files:**
- Create: `internal/tray/tray.go`, `cmd/baobar/main.go`, `README.md` (replace the stub)
- Test: manual, per the steps below

**Interfaces:**
- Consumes: `config.Load`, `config.DefaultPaths`, `bao.Poller`, `bao.NewClient`,
  `bao.ReadToken`, `(*Client).RevokeSelf`, `bao.DeleteCache`, `tray.Label`, `tray.Tooltip`,
  `tray.Icon`, `tray.MenuLines`, `login.Launch`, `login.CLIAvailable`, `notify.NewTracker`,
  `notify.Send`.
- Produces: `tray.Run(opts tray.Options)`.

Three structural rules this task exists to honor:

1. **`o.Status` is called only from the poll goroutine.** It can block for the full HTTP
   timeout, and the goroutine servicing `ClickedCh` must never wait on it.
2. **Configuration failures render in the tray, never on stderr.** There is no terminal
   behind a double-click.
3. **The tray writes only on change.** `render` runs every second; unconditional
   `SetIcon`/`SetTitle` calls are wasted work and flicker on some Linux indicators.

- [ ] **Step 1: Write the tray wiring**

Create `internal/tray/tray.go`:

```go
package tray

import (
	"context"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/BrutalSystems/baobar/internal/bao"
)

type Options struct {
	Addr string

	// ConfigError, when non-empty, puts the tray into a permanent error state:
	// no polling, no login, just the message and Quit. A tray app launched from
	// Finder or Explorer has no terminal, so exiting with a log line would be
	// invisible — and "no VAULT_ADDR yet" is the most likely first-run state.
	ConfigError string

	// CLIAvailable reports whether `bao` is on PATH. M1's login shells out to
	// it; when it is missing the login items say so instead of opening a
	// console that fails.
	CLIAvailable bool

	// Status may block on the network. It is called ONLY from the poll
	// goroutine — never from the goroutine servicing menu clicks, or the menu
	// freezes for the duration of every server check.
	Status     func(context.Context) bao.Status
	Logout     func() error
	Login      func(method string) error
	Refresh    func()
	Thresholds func(tokenKey int64, remaining time.Duration) []time.Duration
	Notify     func(threshold time.Duration)
	OpenURL    func(string) error

	// PollEvery defaults to one second. This is only how often the poller is
	// asked; the poller itself decides when that turns into a request.
	PollEvery time.Duration
}

// holder passes the latest Status from the poll goroutine to the UI goroutine.
type holder struct {
	mu sync.RWMutex
	s  bao.Status
}

func (h *holder) set(s bao.Status) { h.mu.Lock(); h.s = s; h.mu.Unlock() }
func (h *holder) get() bao.Status  { h.mu.RLock(); defer h.mu.RUnlock(); return h.s }

// Run blocks until the user quits. Must be called from main.
func Run(o Options) {
	systray.Run(func() { onReady(o) }, func() {})
}

func onReady(o Options) {
	if o.PollEvery == 0 {
		o.PollEvery = time.Second
	}
	if o.ConfigError != "" {
		onReadyError(o)
		return
	}
	onReadyNormal(o)
}

// onReadyError is the misconfigured state: visible, explicable, and quittable.
func onReadyError(o Options) {
	systray.SetTitle("⚠️ baobar")
	systray.SetTooltip("Baobar: " + o.ConfigError)
	systray.SetIcon(Icon(bao.StateSignedOut))

	mMsg := systray.AddMenuItem(o.ConfigError, "Baobar cannot start until this is fixed")
	mMsg.Disable()
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit Baobar")

	go func() {
		<-mQuit.ClickedCh
		systray.Quit()
	}()
}

func onReadyNormal(o Options) {
	h := &holder{}
	refresh := make(chan struct{}, 1)

	systray.SetIcon(Icon(bao.StateSignedOut))
	systray.SetTitle("🔒 login")
	systray.SetTooltip("OpenBao")

	mAddr := systray.AddMenuItem(o.Addr, "Open the OpenBao web UI")
	systray.AddSeparator()
	mWho := systray.AddMenuItem("Not signed in", "")
	mPolicies := systray.AddMenuItem("", "")
	mExpires := systray.AddMenuItem("", "")
	mWho.Disable()
	mPolicies.Disable()
	mExpires.Disable()
	systray.AddSeparator()
	mLogout := systray.AddMenuItem("Log out (revoke token)", "Revoke this token on the server")
	systray.AddSeparator()
	mUserpass := systray.AddMenuItem("Login with password + TOTP", "Opens a terminal")
	mOIDC := systray.AddMenuItem("Login with SSO", "Opens a terminal")
	systray.AddSeparator()
	mRefresh := systray.AddMenuItem("Refresh now", "Check the server immediately")
	mQuit := systray.AddMenuItem("Quit", "Quit Baobar")

	// Without the CLI, M1 cannot log in at all. Say so once, here, rather than
	// letting the buttons open a console that prints "bao: command not found".
	if !o.CLIAvailable {
		for _, m := range []*systray.MenuItem{mUserpass, mOIDC} {
			m.Disable()
		}
		mUserpass.SetTitle("Login needs the bao CLI (not installed)")
		mOIDC.SetTitle("Use the web UI above to sign in")
	}

	// The poll goroutine is the only caller of o.Status, so a slow or hanging
	// server cannot block menu clicks.
	go func() {
		ctx := context.Background()
		t := time.NewTicker(o.PollEvery)
		defer t.Stop()
		for {
			h.set(o.Status(ctx))
			select {
			case <-t.C:
			case <-refresh:
			}
		}
	}()

	go uiLoop(o, h, refresh, menuItems{
		addr: mAddr, who: mWho, policies: mPolicies, expires: mExpires,
		logout: mLogout, userpass: mUserpass, oidc: mOIDC,
		refresh: mRefresh, quit: mQuit,
	})
}

type menuItems struct {
	addr, who, policies, expires        *systray.MenuItem
	logout, userpass, oidc              *systray.MenuItem
	refresh, quit                       *systray.MenuItem
}

func uiLoop(o Options, h *holder, refreshCh chan struct{}, m menuItems) {
	t := time.NewTicker(time.Second)
	defer t.Stop()

	var (
		lastLabel, lastTooltip           string
		lastWho, lastPolicies, lastExpires string
		lastState                        = bao.State(-1)
	)

	render := func() {
		s := h.get()

		// Recompute the countdown from the absolute expiry so it keeps ticking
		// even while the poll goroutine is blocked on a slow server. The state
		// itself can lag by one poll; the number never does.
		if !s.NeverExpires && !s.ExpiresAt.IsZero() {
			if r := time.Until(s.ExpiresAt); r > 0 {
				s.Remaining = r
			} else {
				s.Remaining = 0
			}
		}

		// Write to the tray only on change: this runs once a second, and
		// rewriting the icon every tick flickers on some Linux indicators.
		if l := Label(s); l != lastLabel {
			systray.SetTitle(l) // renders no text on Windows; see Tooltip
			lastLabel = l
		}
		if tt := Tooltip(s); tt != lastTooltip {
			systray.SetTooltip(tt)
			lastTooltip = tt
		}
		if s.State != lastState {
			systray.SetIcon(Icon(s.State))
		}

		who, policies, expires := MenuLines(s)
		if who != lastWho {
			m.who.SetTitle(who)
			lastWho = who
		}
		if policies != lastPolicies {
			m.policies.SetTitle(policies)
			lastPolicies = policies
		}
		if expires != lastExpires {
			m.expires.SetTitle(expires)
			lastExpires = expires
		}

		if s.State != lastState {
			if policies == "" {
				m.policies.Hide()
			} else {
				m.policies.Show()
			}
			if expires == "" {
				m.expires.Hide()
			} else {
				m.expires.Show()
			}
			if s.State == bao.StateSignedOut {
				m.logout.Hide()
			} else {
				m.logout.Show()
			}
			lastState = s.State
		}

		// One notification per tick: when the app wakes from sleep having
		// crossed both thresholds at once, the most urgent one is the only
		// useful message.
		if !s.NeverExpires && s.Remaining > 0 && !s.ExpiresAt.IsZero() {
			if crossed := o.Thresholds(s.ExpiresAt.Unix(), s.Remaining); len(crossed) > 0 {
				o.Notify(crossed[len(crossed)-1])
			}
		}
	}

	kick := func() {
		select {
		case refreshCh <- struct{}{}:
		default: // a poll is already pending
		}
	}

	render()
	for {
		select {
		case <-t.C:
			render()
		case <-m.addr.ClickedCh:
			_ = o.OpenURL(o.Addr + "/ui")
		case <-m.logout.ClickedCh:
			_ = o.Logout()
			kick()
		case <-m.userpass.ClickedCh:
			_ = o.Login("userpass")
			kick()
		case <-m.oidc.ClickedCh:
			_ = o.Login("oidc")
			kick()
		case <-m.refresh.ClickedCh:
			o.Refresh()
			kick()
		case <-m.quit.ClickedCh:
			systray.Quit()
			return
		}
	}
}
```

- [ ] **Step 2: Write main**

Create `cmd/baobar/main.go`:

```go
// Command baobar shows OpenBao login status in the system tray.
package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"time"

	"github.com/BrutalSystems/baobar/internal/bao"
	"github.com/BrutalSystems/baobar/internal/config"
	"github.com/BrutalSystems/baobar/internal/login"
	"github.com/BrutalSystems/baobar/internal/notify"
	"github.com/BrutalSystems/baobar/internal/tray"
)

func main() {
	// Baobar is usually launched by double-click, where there is no terminal to
	// read. Configuration problems therefore go to the tray, not to stderr —
	// exiting here would look identical to the app simply not starting.
	cfgPath, tokenPath, cachePath, err := config.DefaultPaths()
	if err != nil {
		tray.Run(tray.Options{ConfigError: "Cannot locate your home directory: " + err.Error()})
		return
	}

	cfg, err := config.Load(cfgPath, os.Getenv)
	if err != nil {
		tray.Run(tray.Options{ConfigError: fmt.Sprintf("%v (config: %s)", err, cfgPath)})
		return
	}

	client := bao.NewClient(cfg.Addr)
	poller := &bao.Poller{
		Client:    client,
		TokenPath: tokenPath,
		CachePath: cachePath,
		Recheck:   cfg.Recheck,
		Warn:      cfg.Warn,
		Now:       time.Now,
	}
	tracker := notify.NewTracker(30*time.Minute, 5*time.Minute)

	tray.Run(tray.Options{
		Addr:         cfg.Addr,
		CLIAvailable: login.CLIAvailable(),
		Status:       poller.Status,
		Logout: func() error {
			token, err := bao.ReadToken(tokenPath)
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = client.RevokeSelf(ctx, token)
			}
			_ = bao.DeleteCache(cachePath)
			return removeToken(tokenPath)
		},
		Login: func(method string) error {
			// Drop the cache so the next poll re-checks as soon as login lands.
			_ = bao.DeleteCache(cachePath)
			return login.Launch(cfg.Addr, method)
		},
		Refresh:    func() { _ = bao.DeleteCache(cachePath) },
		Thresholds: tracker.Crossed,
		Notify: func(threshold time.Duration) {
			_ = notify.Send("OpenBao", fmt.Sprintf("Your token expires in less than %s", tray.Human(threshold)))
		},
		OpenURL: openURL,
	})
}

func openURL(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
```

Add `removeToken` to the same file:

```go
func removeToken(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
```

The import block is: `context`, `errors`, `fmt`, `os`, `os/exec`, `runtime`, `time`, plus
the five `internal/` packages. There is no `log` import — nothing in `main` writes to a
terminal that may not exist.

- [ ] **Step 3: Build and run on macOS**

```bash
go get fyne.io/systray
go build ./...
go vet ./...
go test -race ./... -v    # -race matters: the tray runs a poll goroutine beside the UI loop
VAULT_ADDR=https://bao.example.com go run ./cmd/baobar
```

- [ ] **Step 4: Verify by hand on macOS**

Check each, and fix before moving on:

1. Signed out (`mv ~/.vault-token /tmp/`) → menu bar shows `🔒 login`, menu says
   "Not signed in", no logout item.
2. Sign in in a terminal (`bao login -method=oidc role=developer`) → within a second the
   bar shows `🔓` and a countdown, with name and policies in the menu.
3. Countdown decrements every second.
4. **The audit check** — with the app running for ~10 minutes, confirm the server sees
   roughly **2 `lookup-self` requests, not ~20**. Query your OpenBao audit log however you
   normally read it (the audit device writes to stdout, so a log aggregator or
   `kubectl logs` both work). If the count is ~20, the cache is not being read — stop and
   fix it; this is the invariant the whole design rests on. Contributors running against
   their own OpenBao should substitute their own audit tooling here.
5. Unplug the network → icon goes grey, label switches to `🌐`, countdown keeps running,
   and it does **not** say signed out.
6. "Log out (revoke token)" → returns to `🔒 login`, and `bao token lookup` in a terminal
   fails.
7. "Login with password + TOTP" and "Login with SSO" each open Terminal with the right
   command.
8. Set `BAOBAR_WARN=8h` to force the warning state → icon turns amber and a desktop
   notification fires exactly once.
9. `BAOBAR_RECHECK=5s go run ./cmd/baobar` → the tray appears in the ⚠️ error state citing
   the 60s minimum. It must **not** exit silently.
10. **The misconfiguration path** — `env -u VAULT_ADDR go run ./cmd/baobar` with no config
    file → a ⚠️ tray icon whose menu names the problem and the config path it wants, plus a
    working Quit. This is the most likely first-run experience, so it gets a real check.
11. **The responsiveness check** — point Baobar at a black hole so every lookup takes the
    full 5-second timeout, then click through the menu while it hangs:
    ```bash
    VAULT_ADDR=https://10.255.255.1 BAOBAR_RECHECK=60s go run ./cmd/baobar
    ```
    The menu must open and respond immediately, and the countdown (from cache, if any) must
    keep ticking. If clicks lag, the poll goroutine is not doing its job.
12. **The re-login check** — while signed in with a fresh cache, log in again in a terminal
    (`bao login -method=oidc role=developer`). Baobar must show the *new* expiry within a
    second or two, not the old one for the rest of the recheck window. This is the token
    stamp working.
13. **The missing-CLI check** — run with a PATH that lacks `bao`:
    ```bash
    env PATH=/usr/bin:/bin VAULT_ADDR=https://bao.example.com go run ./cmd/baobar
    ```
    Both login items must be disabled and self-explanatory, and the web UI link must still
    work. This is what a fresh Windows machine looks like.

- [ ] **Step 5: Cross-build for Windows and check the Linux requirement**

```bash
# Windows: pure-Go syscalls, no cgo. -H=windowsgui suppresses the console window.
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui" -o dist/baobar.exe ./cmd/baobar

# Linux: needs cgo + GTK3 + libayatana-appindicator3 headers, so this FAILS on
# macOS. Confirm the failure rather than assuming it, then build it on Linux or in CI.
GOOS=linux GOARCH=amd64 go build -o dist/baobar-linux ./cmd/baobar || echo "expected: build Linux on Linux"
```

On a Windows machine, verify the two amendments hold: the tray icon changes color across
states, and the tooltip carries the countdown. **If `SetTitle` turns out to render text on
Windows after all, note it in the spec** — it would mean the icon work is belt-and-braces
rather than load-bearing.

- [ ] **Step 6: Replace the README stub**

Rewrite `README.md` to cover: what it is, the state table, install (build from source for
now), configuration (`VAULT_ADDR`, `BAOBAR_RECHECK`, `BAOBAR_WARN`, `config.toml`), the
audit-log invariant and why not to lower `BAOBAR_RECHECK`, the per-platform build matrix
from Step 5 including the Linux `apt-get install gcc libgtk-3-dev libayatana-appindicator3-dev`
line, and a link to the design doc. Keep the "not yet built" language out of it — by this
point it is built.

- [ ] **Step 7: Commit**

```bash
git add cmd/ internal/tray/tray.go README.md go.mod go.sum
git commit -m "feat: wire the tray, add main, and document the build matrix"
```

---

## Definition of done for M1

- `go test -race ./...` passes; `go vet ./...` is clean.
- All thirteen manual checks in Task 10 Step 4 pass on macOS, including the Loki audit
  check, the misconfiguration path, and the responsiveness check.
- `dist/baobar.exe` cross-builds, and the tray icon plus tooltip have been eyeballed on a
  Windows machine.
- The Linux build has been done on Linux (or in CI) at least once.
- README reflects reality.

Not in M1, by design: in-app login (M2/M3), autostart, token renewal, real artwork,
goreleaser, Homebrew cask, signing and notarization.
