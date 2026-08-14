# Baobar M2 (Terminal-Free Login + Autostart) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Log in to OpenBao from the tray without ever opening a terminal or needing the `bao` CLI, and add a working "Start at login" toggle.

**Architecture:** A new `internal/authflow` package owns one short-lived loopback HTTP server that both auth flows share. OIDC opens the system browser at OpenBao's `auth_url` and catches the provider's redirect; userpass serves its own nonce-protected form and performs OpenBao's two-phase MFA exchange. A new `internal/autostart` package writes one small per-platform artifact behind a three-method interface. `internal/login` — the terminal shell-out — is deleted.

**Tech Stack:** Go 1.26.3, stdlib `net/http` + `net/http/httptest`, `crypto/rand`, `go:embed`, `golang.org/x/sys/windows/registry` (Windows autostart only), existing `fyne.io/systray`.

**Spec:** `docs/superpowers/specs/2026-08-13-baobar-m2-terminal-free-login-design.md` — read it first. It records why the far simpler "just run `bao login` without a console" approach was rejected (it cannot serve a user without the CLI), which is the single decision this whole plan rests on.

## Global Constraints

Every task's requirements implicitly include this section.

- **THIS REPO IS PUBLIC.** No real hostname, username, policy name, cluster/namespace name, token, or absolute path from a private machine in any committed file. Placeholders only: `bao.example.com`, `userpass-dev`, policies `admin`/`deploy`.
- **No credential may ever be logged, cached, placed in a URL, or passed as a process argument.** Passwords and TOTP passcodes exist only in memory for the duration of one request. Only the resulting token is persisted, via `bao.WriteToken` to `~/.vault-token`.
- **The listener binds `127.0.0.1` only** — never `0.0.0.0`, never a hostname. It serves exactly one flow and shuts down on success, failure, or a 5-minute timeout.
- **Never invoke the `bao` CLI.** Not for status, not for login. After this milestone the binary has no dependency on it.
- **Baobar is launched by double-click**, so nothing may write to stderr and exit — failures surface in the tray via `Alert`.
- **`Poller.Status` is only ever called from the tray's poll goroutine.** Auth flows run on their own goroutines and signal completion through the existing refresh channel.
- Go 1.26.3 (pinned in `.tool-versions`); module `github.com/brutalsystems/baobar`.

---

## File Structure

```
internal/config/config.go        MODIFY  five new settings + mount/port validation
internal/authflow/               NEW
  session.go                     one-shot loopback server, nonce, single-flight guard
  oidc.go                        OIDC: auth_url -> browser -> callback -> exchange
  userpass.go                    two-phase MFA API calls (no HTTP serving)
  browser.go                     the served form + its handler
  login.html                     embedded form template
internal/autostart/              NEW
  autostart.go                   interface, New(), shared file-based implementation
  autostart_darwin.go            LaunchAgent plist
  autostart_linux.go             XDG .desktop
  autostart_windows.go           HKCU Run key
internal/tray/tray.go            MODIFY  checkbox item, no CLI branch, flow-in-progress
cmd/baobar/main.go               MODIFY  wire authflow + autostart
internal/login/                  DELETE  entire package
```

`session.go` is the only file that binds a socket. `userpass.go` makes API calls and parses
responses but serves nothing — that split is what makes the two-phase MFA logic testable
without a browser.

---

### Task 1: Config additions

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: existing `config.Config`, `config.Load`, `config.ErrNoAddr`, `config.ErrRecheckTooLow`.
- Produces: `Config.OIDCMount`, `Config.OIDCRole`, `Config.UserpassMount`, `Config.CallbackPort`, `Config.Username`; sentinel `config.ErrBadMount`.

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:

```go
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
		"VAULT_ADDR":             "https://bao.example.com",
		"BAOBAR_OIDC_MOUNT":      "jwt",
		"BAOBAR_OIDC_ROLE":       "developer",
		"BAOBAR_USERPASS_MOUNT":  "userpass2",
		"BAOBAR_CALLBACK_PORT":   "8251",
		"BAOBAR_USERNAME":        "userpass-dev",
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
	for _, bad := range []string{"oidc/../sys", "oidc/x", "oi dc", "oidc?x=1", "oidc#f", ""} {
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadAuth -v`
Expected: FAIL — `c.OIDCMount undefined`.

- [ ] **Step 3: Write the implementation**

In `internal/config/config.go`, add to the `Config` struct:

```go
	// Auth-flow settings. Mounts are path segments in OpenBao request URLs;
	// CallbackPort must match the OIDC role's allowed redirect URI.
	OIDCMount     string
	OIDCRole      string
	UserpassMount string
	CallbackPort  int
	Username      string
```

Add to `fileConfig`:

```go
	OIDCMount     string `toml:"oidc_mount"`
	OIDCRole      string `toml:"oidc_role"`
	UserpassMount string `toml:"userpass_mount"`
	CallbackPort  string `toml:"callback_port"`
	Username      string `toml:"username"`
```

Add the sentinel and the mount pattern near the other package-level vars:

```go
// ErrBadMount means an auth mount path is not a single safe path segment.
var ErrBadMount = errors.New("auth mount must be a single path segment of letters, digits, dashes or underscores")

var mountRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
```

Add these defaults next to `DefaultRecheck`:

```go
const (
	DefaultOIDCMount     = "oidc"
	DefaultUserpassMount = "userpass"
	DefaultCallbackPort  = 8250
)
```

In `Load`, after the existing `Warn` handling and before `return c, nil`:

```go
	c.OIDCMount = firstNonEmpty(fc.OIDCMount, getenv("BAOBAR_OIDC_MOUNT"), DefaultOIDCMount)
	c.UserpassMount = firstNonEmpty(fc.UserpassMount, getenv("BAOBAR_USERPASS_MOUNT"), DefaultUserpassMount)
	for _, m := range []string{c.OIDCMount, c.UserpassMount} {
		if !mountRe.MatchString(m) {
			return Config{}, fmt.Errorf("%w: %q", ErrBadMount, m)
		}
	}

	c.OIDCRole = firstNonEmpty(fc.OIDCRole, getenv("BAOBAR_OIDC_ROLE"))
	c.Username = firstNonEmpty(fc.Username, getenv("BAOBAR_USERNAME"))

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
```

Add `regexp` and `strconv` to the imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/config/
git add internal/config/
git commit -m "feat(config): add auth mount, role, callback port, and username settings"
```

---

### Task 2: The one-shot loopback session

The only file in the project that binds a socket. Everything about "serve one browser
round-trip and then stop" lives here so neither flow re-implements it.

**Files:**
- Create: `internal/authflow/session.go`
- Test: `internal/authflow/session_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `authflow.ErrBusy`, `authflow.ErrTimeout`, and unexported `newSession(port int, timeout time.Duration) (*session, error)`, `(*session).baseURL() string`, `(*session).handle(pattern string, h http.HandlerFunc)`, `(*session).finish(token string, err error)`, `(*session).serve() (string, error)`, `acquire() bool`, `release()`, `nonce() (string, error)`, `writePage(w http.ResponseWriter, title, message string)`.

- [ ] **Step 1: Write the failing test**

Create `internal/authflow/session_test.go`:

```go
package authflow

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSessionBindsLoopbackOnly(t *testing.T) {
	s, err := newSession(0, time.Minute)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	defer s.finish("", nil)

	if !strings.HasPrefix(s.baseURL(), "http://127.0.0.1:") {
		t.Errorf("baseURL = %q, want a loopback address", s.baseURL())
	}
}

func TestSessionServeReturnsTheFinishedToken(t *testing.T) {
	s, err := newSession(0, time.Minute)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	s.handle("/done", func(w http.ResponseWriter, r *http.Request) {
		s.finish("hvs.abc", nil)
	})

	go func() {
		http.Get(s.baseURL() + "/done")
	}()

	token, err := s.serve()
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	if token != "hvs.abc" {
		t.Errorf("token = %q, want hvs.abc", token)
	}
}

func TestSessionServeReturnsTheFinishedError(t *testing.T) {
	s, err := newSession(0, time.Minute)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	want := errors.New("provider said no")
	go s.finish("", want)

	if _, err := s.serve(); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// A listener left running is a standing local endpoint; the timeout is a
// security property, not a convenience.
func TestSessionTimesOut(t *testing.T) {
	s, err := newSession(0, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}

	start := time.Now()
	if _, err := s.serve(); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("serve took %v, want it to give up promptly", elapsed)
	}
}

// The browser can hit the callback more than once (reloads, prefetch).
func TestSessionFinishIsIdempotent(t *testing.T) {
	s, err := newSession(0, time.Minute)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	go func() {
		s.finish("first", nil)
		s.finish("second", nil)
		s.finish("", errors.New("third"))
	}()

	token, err := s.serve()
	if err != nil || token != "first" {
		t.Fatalf("got %q, %v — want the first result to win", token, err)
	}
}

func TestSessionRefusesAnOccupiedPort(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port

	_, err = newSession(port, time.Minute)
	if err == nil {
		t.Fatal("expected an error binding an occupied port")
	}
	if !strings.Contains(err.Error(), "already in use") && !strings.Contains(err.Error(), "address already in use") {
		t.Logf("error was: %v", err)
	}
}

// Only one flow at a time: two browser windows racing to write the token file
// is not a state worth supporting.
func TestSingleFlight(t *testing.T) {
	if !acquire() {
		t.Fatal("first acquire failed")
	}
	if acquire() {
		t.Error("second acquire succeeded while a flow was live")
	}
	release()
	if !acquire() {
		t.Error("acquire failed after release")
	}
	release()
}

func TestNonceIsRandomAndLongEnough(t *testing.T) {
	a, err := nonce()
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	if len(a) != 64 {
		t.Errorf("nonce length = %d, want 64 hex chars", len(a))
	}
	b, _ := nonce()
	if a == b {
		t.Error("two nonces were identical")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/authflow/ -v`
Expected: FAIL — `undefined: newSession`.

- [ ] **Step 3: Write the implementation**

Create `internal/authflow/session.go`:

```go
// Package authflow acquires an OpenBao token by driving the system browser
// through a login flow and catching the result on a short-lived loopback
// listener. It never invokes the bao CLI and never opens a terminal.
package authflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrBusy means another login flow is already running.
	ErrBusy = errors.New("a login is already in progress")
	// ErrTimeout means the browser never came back in time.
	ErrTimeout = errors.New("login timed out")
)

// DefaultTimeout bounds how long a listener may stay open.
const DefaultTimeout = 5 * time.Minute

var inFlight atomic.Bool

// acquire reserves the single login slot, returning false if one is taken.
func acquire() bool { return inFlight.CompareAndSwap(false, true) }

func release() { inFlight.Store(false) }

// nonce returns 32 random bytes, hex encoded.
func nonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

type outcome struct {
	token string
	err   error
}

// session is one browser round-trip: a loopback listener serving a handful of
// routes until something calls finish, or the timeout expires.
type session struct {
	ln      net.Listener
	mux     *http.ServeMux
	srv     *http.Server
	done    chan outcome
	once    sync.Once
	timeout time.Duration
}

// newSession binds 127.0.0.1 on port (0 for any free port).
func newSession(port int, timeout time.Duration) (*session, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("listen on 127.0.0.1:%d: %w", port, err)
	}
	mux := http.NewServeMux()
	return &session{
		ln:      ln,
		mux:     mux,
		srv:     &http.Server{Handler: noStore(mux)},
		done:    make(chan outcome, 1),
		timeout: timeout,
	}, nil
}

// noStore keeps every page of a login flow out of caches and histories.
func noStore(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	})
}

func (s *session) baseURL() string {
	return "http://" + s.ln.Addr().String()
}

func (s *session) handle(pattern string, h http.HandlerFunc) {
	s.mux.HandleFunc(pattern, h)
}

// finish records the flow's result. Only the first call counts: a browser may
// hit the callback more than once.
func (s *session) finish(token string, err error) {
	s.once.Do(func() { s.done <- outcome{token: token, err: err} })
}

// serve runs the listener until finish or timeout, then shuts it down.
func (s *session) serve() (string, error) {
	go s.srv.Serve(s.ln)

	timer := time.NewTimer(s.timeout)
	defer timer.Stop()

	var out outcome
	select {
	case out = <-s.done:
	case <-timer.C:
		out = outcome{err: ErrTimeout}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)

	return out.token, out.err
}

// writePage renders the minimal end-of-flow page shown in the browser. It never
// contains a token, an identity, or a server address.
func writePage(w http.ResponseWriter, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>%s</title>"+
		"<body style=\"font:16px system-ui;padding:3rem;max-width:32rem;margin:auto\">"+
		"<h1 style=\"font-size:1.25rem\">%s</h1><p>%s</p>", title, title, message)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/authflow/ -v`
Expected: PASS, 8 tests.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/authflow/
git add internal/authflow/
git commit -m "feat(authflow): one-shot loopback session with timeout and single-flight guard"
```

---

### Task 3: OIDC flow

**Files:**
- Create: `internal/authflow/oidc.go`
- Test: `internal/authflow/oidc_test.go`

**Interfaces:**
- Consumes: `newSession`, `nonce`, `acquire`, `release`, `ErrBusy`, `DefaultTimeout`.
- Produces: `authflow.OIDCConfig{Addr, Mount, Role string; CallbackPort int; HTTP *http.Client; OpenURL func(string) error; Timeout time.Duration}` and `authflow.OIDC(ctx context.Context, cfg OIDCConfig) (string, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/authflow/oidc_test.go`:

```go
package authflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeBao stands in for OpenBao's OIDC endpoints.
func fakeBao(t *testing.T, authURLFor func(redirect string) string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oidc/auth_url"):
			var body struct {
				Role        string `json:"role"`
				RedirectURI string `json:"redirect_uri"`
				ClientNonce string `json:"client_nonce"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.RedirectURI == "" || body.ClientNonce == "" {
				t.Errorf("auth_url request missing fields: %+v", body)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"auth_url": authURLFor(body.RedirectURI)},
			})
		case strings.HasSuffix(r.URL.Path, "/oidc/callback"):
			q := r.URL.Query()
			if q.Get("code") == "" || q.Get("state") == "" || q.Get("client_nonce") == "" {
				t.Errorf("callback exchange missing params: %v", q)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"auth": map[string]any{"client_token": "hvs.oidc-token"},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestOIDCHappyPath(t *testing.T) {
	bao := fakeBao(t, func(redirect string) string {
		// The "provider" will send the browser straight back with code+state.
		return redirect + "?code=abc&state=xyz"
	})
	defer bao.Close()

	var opened string
	cfg := OIDCConfig{
		Addr: bao.URL, Mount: "oidc", Role: "developer", CallbackPort: 0,
		Timeout: 5 * time.Second,
		OpenURL: func(u string) error {
			opened = u
			go http.Get(u) // stand in for the browser following the redirect
			return nil
		},
	}

	token, err := OIDC(context.Background(), cfg)
	if err != nil {
		t.Fatalf("OIDC: %v", err)
	}
	if token != "hvs.oidc-token" {
		t.Errorf("token = %q", token)
	}
	if !strings.Contains(opened, "code=abc") {
		t.Errorf("browser was sent to %q", opened)
	}
}

func TestOIDCProviderError(t *testing.T) {
	bao := fakeBao(t, func(redirect string) string {
		return redirect + "?error=access_denied&error_description=nope"
	})
	defer bao.Close()

	cfg := OIDCConfig{
		Addr: bao.URL, Mount: "oidc", CallbackPort: 0, Timeout: 5 * time.Second,
		OpenURL: func(u string) error { go http.Get(u); return nil },
	}

	if _, err := OIDC(context.Background(), cfg); err == nil {
		t.Fatal("expected an error when the provider denies access")
	} else if !strings.Contains(err.Error(), "access_denied") {
		t.Errorf("err = %v, want it to name the provider's error", err)
	}
}

func TestOIDCTimesOutWhenTheBrowserNeverReturns(t *testing.T) {
	bao := fakeBao(t, func(redirect string) string { return "http://example.invalid/never" })
	defer bao.Close()

	cfg := OIDCConfig{
		Addr: bao.URL, Mount: "oidc", CallbackPort: 0, Timeout: 100 * time.Millisecond,
		OpenURL: func(u string) error { return nil }, // browser never comes back
	}

	if _, err := OIDC(context.Background(), cfg); err != ErrTimeout {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

func TestOIDCRefusesConcurrentFlows(t *testing.T) {
	if !acquire() {
		t.Fatal("could not take the slot")
	}
	defer release()

	_, err := OIDC(context.Background(), OIDCConfig{Addr: "https://bao.example.com"})
	if err != ErrBusy {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

// The role is optional: a mount with a default_role configured takes none.
func TestOIDCOmitsAnEmptyRole(t *testing.T) {
	var sawRole bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/auth_url") {
			var m map[string]any
			json.NewDecoder(r.Body).Decode(&m)
			_, sawRole = m["role"]
			u, _ := url.Parse("http://127.0.0.1:1/never")
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"auth_url": u.String()}})
		}
	}))
	defer srv.Close()

	cfg := OIDCConfig{
		Addr: srv.URL, Mount: "oidc", Role: "", CallbackPort: 0,
		Timeout: 50 * time.Millisecond,
		OpenURL: func(string) error { return nil },
	}
	OIDC(context.Background(), cfg)

	if sawRole {
		t.Error("an empty role was sent; it must be omitted entirely")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/authflow/ -run TestOIDC -v`
Expected: FAIL — `undefined: OIDCConfig`.

- [ ] **Step 3: Write the implementation**

Create `internal/authflow/oidc.go`:

```go
package authflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// OIDCConfig describes one SSO login attempt.
type OIDCConfig struct {
	Addr  string // OpenBao base URL, no trailing slash
	Mount string // auth mount path, e.g. "oidc"
	Role  string // optional; omitted from the request when empty
	// CallbackPort must match the role's allowed redirect URI. It is not
	// randomised: a different port would fail the provider's redirect check
	// later, far from the cause.
	CallbackPort int
	HTTP         *http.Client
	OpenURL      func(string) error
	Timeout      time.Duration
}

func (c OIDCConfig) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// OIDC drives the browser through an OpenBao OIDC login and returns the token.
func OIDC(ctx context.Context, cfg OIDCConfig) (string, error) {
	if !acquire() {
		return "", ErrBusy
	}
	defer release()

	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}

	clientNonce, err := nonce()
	if err != nil {
		return "", err
	}

	s, err := newSession(cfg.CallbackPort, cfg.Timeout)
	if err != nil {
		return "", fmt.Errorf("cannot listen for the OIDC redirect on port %d "+
			"(it must match the role's allowed redirect URI): %w", cfg.CallbackPort, err)
	}

	redirectURI := s.baseURL() + "/oidc/callback"

	authURL, err := cfg.authURL(ctx, redirectURI, clientNonce)
	if err != nil {
		s.finish("", err)
		s.serve()
		return "", err
	}

	s.handle("/oidc/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			desc := q.Get("error_description")
			writePage(w, "Login failed", "You can close this tab.")
			s.finish("", fmt.Errorf("identity provider returned %s: %s", e, desc))
			return
		}
		token, err := cfg.exchange(ctx, q.Get("state"), q.Get("code"), clientNonce)
		if err != nil {
			writePage(w, "Login failed", "You can close this tab.")
			s.finish("", err)
			return
		}
		writePage(w, "Signed in", "You can close this tab and return to Baobar.")
		s.finish(token, nil)
	})

	if err := cfg.OpenURL(authURL); err != nil {
		s.finish("", fmt.Errorf("open browser: %w", err))
	}

	return s.serve()
}

func (c OIDCConfig) authURL(ctx context.Context, redirectURI, clientNonce string) (string, error) {
	payload := map[string]string{
		"redirect_uri": redirectURI,
		"client_nonce": clientNonce,
	}
	if c.Role != "" {
		payload["role"] = c.Role
	}
	b, _ := json.Marshal(payload)

	endpoint := fmt.Sprintf("%s/v1/auth/%s/oidc/auth_url", c.Addr, c.Mount)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("request auth URL: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request auth URL: unexpected status %d", resp.StatusCode)
	}

	var body struct {
		Data struct {
			AuthURL string `json:"auth_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode auth URL: %w", err)
	}
	if body.Data.AuthURL == "" {
		return "", fmt.Errorf("OpenBao returned no auth URL (is the %q mount configured?)", c.Mount)
	}
	return body.Data.AuthURL, nil
}

func (c OIDCConfig) exchange(ctx context.Context, state, code, clientNonce string) (string, error) {
	q := url.Values{}
	q.Set("state", state)
	q.Set("code", code)
	q.Set("client_nonce", clientNonce)

	endpoint := fmt.Sprintf("%s/v1/auth/%s/oidc/callback?%s", c.Addr, c.Mount, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("exchange code: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("exchange code: unexpected status %d", resp.StatusCode)
	}

	var body struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	if body.Auth.ClientToken == "" {
		return "", fmt.Errorf("OpenBao returned no token")
	}
	return body.Auth.ClientToken, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/authflow/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/authflow/
git add internal/authflow/
git commit -m "feat(authflow): in-process OIDC login via browser and loopback callback"
```

---

### Task 4: Userpass two-phase MFA calls

API calls and response parsing only — no HTTP serving. Keeping this separate is what
makes the MFA logic testable without a browser.

**Files:**
- Create: `internal/authflow/userpass.go`
- Test: `internal/authflow/userpass_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `authflow.UserpassConfig{Addr, Mount string; HTTP *http.Client}`, `authflow.ErrBadCredentials`, `authflow.ErrBadPasscode`, and unexported `mfaChallenge{RequestID, MethodKey string}`, `(UserpassConfig).login(ctx, user, password string) (string, *mfaChallenge, error)`, `(UserpassConfig).validate(ctx context.Context, ch *mfaChallenge, passcode string) (string, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/authflow/userpass_test.go`:

```go
package authflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Not every user has MFA enforced; phase one alone must be able to succeed.
func TestUserpassLoginWithoutMFA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/auth/userpass/login/userpass-dev") {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"auth": map[string]any{"client_token": "hvs.no-mfa"},
		})
	}))
	defer srv.Close()

	cfg := UserpassConfig{Addr: srv.URL, Mount: "userpass"}
	token, ch, err := cfg.login(context.Background(), "userpass-dev", "pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if ch != nil {
		t.Errorf("challenge = %+v, want nil", ch)
	}
	if token != "hvs.no-mfa" {
		t.Errorf("token = %q", token)
	}
}

func TestUserpassLoginReturnsMFAChallengeByUUID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"auth":{"client_token":"","mfa_requirement":{
			"mfa_request_id":"req-1",
			"mfa_constraints":{"enforceUserpass":{"any":[
				{"type":"totp","id":"uuid-1","uses_passcode":true,"name":"my_totp"}]}}}}}`))
	}))
	defer srv.Close()

	cfg := UserpassConfig{Addr: srv.URL, Mount: "userpass"}
	token, ch, err := cfg.login(context.Background(), "userpass-dev", "pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if token != "" {
		t.Errorf("token = %q, want empty while MFA is pending", token)
	}
	if ch == nil || ch.RequestID != "req-1" || ch.MethodKey != "uuid-1" {
		t.Fatalf("challenge = %+v, want request req-1 keyed by uuid-1", ch)
	}
}

// The API accepts the method name as the payload key too; use it when no UUID
// is supplied rather than failing.
func TestUserpassChallengeFallsBackToMethodName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"auth":{"mfa_requirement":{
			"mfa_request_id":"req-2",
			"mfa_constraints":{"e":{"any":[
				{"type":"totp","id":"","uses_passcode":true,"name":"named_method"}]}}}}}`))
	}))
	defer srv.Close()

	cfg := UserpassConfig{Addr: srv.URL, Mount: "userpass"}
	_, ch, err := cfg.login(context.Background(), "userpass-dev", "pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if ch == nil || ch.MethodKey != "named_method" {
		t.Fatalf("challenge = %+v, want the method name as the key", ch)
	}
}

func TestUserpassBadPassword(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"errors":["invalid username or password"]}`))
	}))
	defer srv.Close()

	cfg := UserpassConfig{Addr: srv.URL, Mount: "userpass"}
	if _, _, err := cfg.login(context.Background(), "userpass-dev", "wrong"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("err = %v, want ErrBadCredentials", err)
	}
}

func TestUserpassValidateReturnsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sys/mfa/validate" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body struct {
			RequestID string              `json:"mfa_request_id"`
			Payload   map[string][]string `json:"mfa_payload"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.RequestID != "req-1" || len(body.Payload["uuid-1"]) != 1 || body.Payload["uuid-1"][0] != "910201" {
			t.Errorf("validate body = %+v", body)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"auth": map[string]any{"client_token": "hvs.mfa-token"},
		})
	}))
	defer srv.Close()

	cfg := UserpassConfig{Addr: srv.URL, Mount: "userpass"}
	ch := &mfaChallenge{RequestID: "req-1", MethodKey: "uuid-1"}
	token, err := cfg.validate(context.Background(), ch, "910201")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if token != "hvs.mfa-token" {
		t.Errorf("token = %q", token)
	}
}

// A wrong passcode must be distinguishable so the form can re-prompt rather
// than dumping the user back to the menu.
func TestUserpassBadPasscode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"errors":["failed to validate MFA"]}`))
	}))
	defer srv.Close()

	cfg := UserpassConfig{Addr: srv.URL, Mount: "userpass"}
	ch := &mfaChallenge{RequestID: "req-1", MethodKey: "uuid-1"}
	if _, err := cfg.validate(context.Background(), ch, "000000"); !errors.Is(err, ErrBadPasscode) {
		t.Fatalf("err = %v, want ErrBadPasscode", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/authflow/ -run TestUserpass -v`
Expected: FAIL — `undefined: UserpassConfig`.

- [ ] **Step 3: Write the implementation**

Create `internal/authflow/userpass.go`:

```go
package authflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var (
	// ErrBadCredentials means the username or password was rejected.
	ErrBadCredentials = errors.New("invalid username or password")
	// ErrBadPasscode means the TOTP passcode was rejected.
	ErrBadPasscode = errors.New("invalid passcode")
)

// UserpassConfig describes one username/password login attempt.
type UserpassConfig struct {
	Addr  string
	Mount string
	HTTP  *http.Client
}

func (c UserpassConfig) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// mfaChallenge is phase one's answer when MFA is enforced. MethodKey is the
// method's UUID when the server supplies one, otherwise its name — the API
// accepts either as the mfa_payload key.
type mfaChallenge struct {
	RequestID string
	MethodKey string
}

// authResponse covers both a completed login and an MFA-pending one.
type authResponse struct {
	Auth struct {
		ClientToken    string `json:"client_token"`
		MFARequirement *struct {
			MFARequestID   string `json:"mfa_request_id"`
			MFAConstraints map[string]struct {
				Any []struct {
					Type         string `json:"type"`
					ID           string `json:"id"`
					Name         string `json:"name"`
					UsesPasscode bool   `json:"uses_passcode"`
				} `json:"any"`
			} `json:"mfa_constraints"`
		} `json:"mfa_requirement"`
	} `json:"auth"`
}

// login performs phase one. It returns either a token (no MFA configured) or a
// challenge (MFA required) — never both.
func (c UserpassConfig) login(ctx context.Context, username, password string) (string, *mfaChallenge, error) {
	body, _ := json.Marshal(map[string]string{"password": password})
	endpoint := fmt.Sprintf("%s/v1/auth/%s/login/%s", c.Addr, c.Mount, username)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client().Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return "", nil, ErrBadCredentials
	default:
		return "", nil, fmt.Errorf("login: unexpected status %d", resp.StatusCode)
	}

	var ar authResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return "", nil, fmt.Errorf("decode login response: %w", err)
	}

	if ar.Auth.ClientToken != "" {
		return ar.Auth.ClientToken, nil, nil
	}
	if ar.Auth.MFARequirement == nil {
		return "", nil, errors.New("login returned neither a token nor an MFA requirement")
	}

	for _, constraint := range ar.Auth.MFARequirement.MFAConstraints {
		for _, m := range constraint.Any {
			if !m.UsesPasscode {
				continue
			}
			key := m.ID
			if key == "" {
				key = m.Name
			}
			if key == "" {
				continue
			}
			return "", &mfaChallenge{
				RequestID: ar.Auth.MFARequirement.MFARequestID,
				MethodKey: key,
			}, nil
		}
	}
	return "", nil, errors.New("MFA is required but no passcode method was offered")
}

// validate performs phase two, exchanging the passcode for a token.
func (c UserpassConfig) validate(ctx context.Context, ch *mfaChallenge, passcode string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"mfa_request_id": ch.RequestID,
		"mfa_payload":    map[string][]string{ch.MethodKey: {passcode}},
	})

	endpoint := c.Addr + "/v1/sys/mfa/validate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("validate request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return "", ErrBadPasscode
	default:
		return "", fmt.Errorf("validate: unexpected status %d", resp.StatusCode)
	}

	var ar authResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return "", fmt.Errorf("decode validate response: %w", err)
	}
	if ar.Auth.ClientToken == "" {
		return "", errors.New("MFA validation returned no token")
	}
	return ar.Auth.ClientToken, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/authflow/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/authflow/
git add internal/authflow/
git commit -m "feat(authflow): two-phase userpass + TOTP login against the OpenBao API"
```

---

### Task 5: The browser-served login form

**Files:**
- Create: `internal/authflow/browser.go`, `internal/authflow/login.html`
- Test: `internal/authflow/browser_test.go`

**Interfaces:**
- Consumes: `UserpassConfig`, `mfaChallenge`, `ErrBadCredentials`, `ErrBadPasscode`, `newSession`, `nonce`, `acquire`, `release`, `ErrBusy`, `writePage`.
- Produces: `authflow.UserpassBrowserConfig{Userpass UserpassConfig; DefaultUsername string; OpenURL func(string) error; Timeout time.Duration}` and `authflow.UserpassBrowser(ctx context.Context, cfg UserpassBrowserConfig) (string, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/authflow/browser_test.go`:

```go
package authflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// mfaBao is a fake OpenBao that demands a TOTP passcode of "910201".
func mfaBao(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/login/"):
			w.Write([]byte(`{"auth":{"mfa_requirement":{"mfa_request_id":"req-1",
				"mfa_constraints":{"e":{"any":[{"type":"totp","id":"uuid-1","uses_passcode":true}]}}}}}`))
		case r.URL.Path == "/v1/sys/mfa/validate":
			var body struct {
				Payload map[string][]string `json:"mfa_payload"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Payload["uuid-1"][0] != "910201" {
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"errors":["failed to validate MFA"]}`))
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"auth": map[string]any{"client_token": "hvs.form-token"},
			})
		}
	}))
}

func TestUserpassBrowserHappyPath(t *testing.T) {
	bao := mfaBao(t)
	defer bao.Close()

	done := make(chan struct{})
	cfg := UserpassBrowserConfig{
		Userpass:        UserpassConfig{Addr: bao.URL, Mount: "userpass"},
		DefaultUsername: "userpass-dev",
		Timeout:         5 * time.Second,
		OpenURL: func(u string) error {
			go func() {
				defer close(done)
				// The "browser" fetches the form, then submits it.
				resp, err := http.Get(u)
				if err != nil {
					t.Errorf("GET form: %v", err)
					return
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if !strings.Contains(string(body), `value="userpass-dev"`) {
					t.Error("form did not prefill the configured username")
				}
				if !strings.Contains(string(body), `autocomplete="off"`) {
					t.Error("form is missing autocomplete=off")
				}
				http.PostForm(u, url.Values{
					"username": {"userpass-dev"}, "password": {"pw"}, "passcode": {"910201"},
				})
			}()
			return nil
		},
	}

	token, err := UserpassBrowser(context.Background(), cfg)
	<-done
	if err != nil {
		t.Fatalf("UserpassBrowser: %v", err)
	}
	if token != "hvs.form-token" {
		t.Errorf("token = %q", token)
	}
}

// A wrong passcode re-renders the form with an error instead of ending the flow.
func TestUserpassBrowserWrongPasscodeRePrompts(t *testing.T) {
	bao := mfaBao(t)
	defer bao.Close()

	cfg := UserpassBrowserConfig{
		Userpass: UserpassConfig{Addr: bao.URL, Mount: "userpass"},
		Timeout:  5 * time.Second,
		OpenURL: func(u string) error {
			go func() {
				resp, _ := http.PostForm(u, url.Values{
					"username": {"userpass-dev"}, "password": {"pw"}, "passcode": {"000000"},
				})
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Errorf("status = %d, want 200 with a re-rendered form", resp.StatusCode)
				}
				if !strings.Contains(string(body), "passcode") {
					t.Error("re-rendered page is not the form")
				}
				if strings.Contains(string(body), "pw") {
					t.Error("re-rendered form echoed the password back")
				}
				// Now succeed.
				http.PostForm(u, url.Values{
					"username": {"userpass-dev"}, "password": {"pw"}, "passcode": {"910201"},
				})
			}()
			return nil
		},
	}

	token, err := UserpassBrowser(context.Background(), cfg)
	if err != nil {
		t.Fatalf("UserpassBrowser: %v", err)
	}
	if token != "hvs.form-token" {
		t.Errorf("token = %q, want the retry to succeed", token)
	}
}

// The nonce is what stops any other local process from driving the flow.
func TestUserpassBrowserRejectsAWrongNonce(t *testing.T) {
	bao := mfaBao(t)
	defer bao.Close()

	got := make(chan int, 1)
	cfg := UserpassBrowserConfig{
		Userpass: UserpassConfig{Addr: bao.URL, Mount: "userpass"},
		Timeout:  300 * time.Millisecond,
		OpenURL: func(u string) error {
			go func() {
				parsed, _ := url.Parse(u)
				resp, err := http.Get(parsed.Scheme + "://" + parsed.Host + "/login/deadbeef")
				if err == nil {
					got <- resp.StatusCode
					resp.Body.Close()
				}
			}()
			return nil
		},
	}

	UserpassBrowser(context.Background(), cfg)

	select {
	case code := <-got:
		if code != http.StatusNotFound {
			t.Errorf("guessed path returned %d, want 404", code)
		}
	case <-time.After(time.Second):
		t.Error("no response observed")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/authflow/ -run TestUserpassBrowser -v`
Expected: FAIL — `undefined: UserpassBrowserConfig`.

- [ ] **Step 3: Write the form template**

Create `internal/authflow/login.html`:

```html
<!doctype html>
<meta charset="utf-8">
<title>Sign in to OpenBao</title>
<body style="font:16px system-ui;padding:3rem;max-width:22rem;margin:auto">
<h1 style="font-size:1.25rem">Sign in to OpenBao</h1>
{{if .Error}}<p style="color:#c0392b" role="alert">{{.Error}}</p>{{end}}
<form method="post" autocomplete="off">
  <label style="display:block;margin:.75rem 0 .25rem">Username</label>
  <input name="username" value="{{.Username}}" autocomplete="off" required
         style="width:100%;padding:.5rem;box-sizing:border-box">
  <label style="display:block;margin:.75rem 0 .25rem">Password</label>
  <input name="password" type="password" autocomplete="off" required
         style="width:100%;padding:.5rem;box-sizing:border-box">
  <label style="display:block;margin:.75rem 0 .25rem">TOTP code</label>
  <input name="passcode" inputmode="numeric" autocomplete="off"
         style="width:100%;padding:.5rem;box-sizing:border-box">
  <button type="submit" style="margin-top:1rem;padding:.5rem 1rem">Sign in</button>
</form>
<p style="color:#666;font-size:.85rem;margin-top:1.5rem">
  This page is served by Baobar on your own machine and closes when you are signed in.
</p>
```

- [ ] **Step 4: Write the implementation**

Create `internal/authflow/browser.go`:

```go
package authflow

import (
	"context"
	_ "embed"
	"errors"
	"html/template"
	"net/http"
	"time"
)

//go:embed login.html
var loginHTML string

var loginTmpl = template.Must(template.New("login").Parse(loginHTML))

// UserpassBrowserConfig describes one form-based login attempt.
type UserpassBrowserConfig struct {
	Userpass        UserpassConfig
	DefaultUsername string
	OpenURL         func(string) error
	Timeout         time.Duration
}

type formData struct {
	Username string
	Error    string
}

// UserpassBrowser serves a login form on loopback, opens the browser at it, and
// returns the token once the user submits valid credentials. The password and
// passcode live only in the request they arrive on.
func UserpassBrowser(ctx context.Context, cfg UserpassBrowserConfig) (string, error) {
	if !acquire() {
		return "", ErrBusy
	}
	defer release()

	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}

	n, err := nonce()
	if err != nil {
		return "", err
	}

	s, err := newSession(0, cfg.Timeout)
	if err != nil {
		return "", err
	}

	path := "/login/" + n
	s.handle(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			render(w, formData{Username: cfg.DefaultUsername})
			return
		}

		if err := r.ParseForm(); err != nil {
			render(w, formData{Username: cfg.DefaultUsername, Error: "Could not read the form."})
			return
		}
		username := r.PostFormValue("username")
		password := r.PostFormValue("password")
		passcode := r.PostFormValue("passcode")

		token, ch, err := cfg.Userpass.login(ctx, username, password)
		switch {
		case errors.Is(err, ErrBadCredentials):
			render(w, formData{Username: username, Error: "Invalid username or password."})
			return
		case err != nil:
			render(w, formData{Username: username, Error: "Could not reach OpenBao."})
			return
		}

		if ch != nil {
			token, err = cfg.Userpass.validate(ctx, ch, passcode)
			switch {
			case errors.Is(err, ErrBadPasscode):
				render(w, formData{Username: username, Error: "That passcode was not accepted."})
				return
			case err != nil:
				render(w, formData{Username: username, Error: "Could not validate the passcode."})
				return
			}
		}

		writePage(w, "Signed in", "You can close this tab and return to Baobar.")
		s.finish(token, nil)
	})

	if err := cfg.OpenURL(s.baseURL() + path); err != nil {
		s.finish("", err)
	}

	return s.serve()
}

func render(w http.ResponseWriter, d formData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = loginTmpl.Execute(w, d)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/authflow/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/authflow/
git add internal/authflow/
git commit -m "feat(authflow): nonce-protected login form served on loopback"
```

---

### Task 6: Autostart

**Files:**
- Create: `internal/autostart/autostart.go`, `internal/autostart/autostart_darwin.go`, `internal/autostart/autostart_linux.go`, `internal/autostart/autostart_windows.go`
- Test: `internal/autostart/autostart_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `autostart.Autostart` interface with `Enabled() (bool, error)`, `Enable() error`, `Disable() error`; `autostart.New() (Autostart, error)`; and the shared unexported `fileAutostart{path string, render func(exe string) []byte}`.

- [ ] **Step 1: Write the failing test**

Create `internal/autostart/autostart_test.go`:

```go
package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestFileAutostart(t *testing.T) (*fileAutostart, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "baobar.entry")
	return &fileAutostart{
		path: path,
		exe:  func() (string, error) { return "/opt/baobar", nil },
		render: func(exe string) []byte {
			return []byte("run " + exe + "\n")
		},
	}, path
}

func TestEnableCreatesTheEntryAndReportsEnabled(t *testing.T) {
	a, path := newTestFileAutostart(t)

	if on, err := a.Enabled(); err != nil || on {
		t.Fatalf("Enabled() = %v, %v — want false before enabling", on, err)
	}
	if err := a.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("entry not written: %v", err)
	}
	if !strings.Contains(string(b), "/opt/baobar") {
		t.Errorf("entry does not reference the executable: %s", b)
	}

	if on, err := a.Enabled(); err != nil || !on {
		t.Errorf("Enabled() = %v, %v — want true after enabling", on, err)
	}
}

func TestDisableRemovesTheEntryAndIsIdempotent(t *testing.T) {
	a, path := newTestFileAutostart(t)
	if err := a.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := a.Disable(); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("entry still present after Disable")
	}
	if err := a.Disable(); err != nil {
		t.Errorf("second Disable: %v — must be idempotent", err)
	}
}

// Enabling can fail (read-only home, sandbox). It must report the failure
// rather than leaving the UI claiming a state that was never achieved.
func TestEnableFailureIsReportedAndLeavesItDisabled(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := &fileAutostart{
		path:   filepath.Join(blocker, "baobar.entry"), // parent is a file
		exe:    func() (string, error) { return "/opt/baobar", nil },
		render: func(exe string) []byte { return []byte(exe) },
	}

	if err := a.Enable(); err == nil {
		t.Fatal("expected Enable to fail when the entry cannot be written")
	}
	if on, _ := a.Enabled(); on {
		t.Error("Enabled() reports true after a failed Enable")
	}
}

func TestNewReturnsSomethingUsable(t *testing.T) {
	a, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := a.Enabled(); err != nil {
		t.Errorf("Enabled: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/autostart/ -v`
Expected: FAIL — `undefined: fileAutostart`.

- [ ] **Step 3: Write the shared implementation**

Create `internal/autostart/autostart.go`:

```go
// Package autostart registers Baobar to start when the user logs in.
//
// Each platform gets one small artifact. The interface deliberately reports
// real on-disk state rather than a remembered flag: a checkbox showing "on"
// for something that never happened is worse than no checkbox at all.
package autostart

import (
	"errors"
	"os"
	"path/filepath"
)

type Autostart interface {
	// Enabled reports whether the entry currently exists.
	Enabled() (bool, error)
	// Enable creates or replaces the entry, pointing at this executable.
	Enable() error
	// Disable removes the entry. Removing an absent entry is not an error.
	Disable() error
}

// fileAutostart covers every platform whose mechanism is "write a file".
type fileAutostart struct {
	path   string
	exe    func() (string, error)
	render func(exe string) []byte
}

func (a *fileAutostart) Enabled() (bool, error) {
	_, err := os.Stat(a.path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (a *fileAutostart) Enable() error {
	exe, err := a.exe()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(a.path, a.render(exe), 0o644)
}

func (a *fileAutostart) Disable() error {
	err := os.Remove(a.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
```

Create `internal/autostart/autostart_darwin.go`:

```go
package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

const label = "com.brutalsystems.baobar"

// New returns a LaunchAgent-backed autostart. A plist works for an unbundled
// binary; SMAppService would require an app bundle.
func New() (Autostart, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &fileAutostart{
		path: filepath.Join(home, "Library", "LaunchAgents", label+".plist"),
		exe:  os.Executable,
		render: func(exe string) []byte {
			return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key><array><string>%s</string></array>
	<key>RunAtLoad</key><true/>
</dict>
</plist>
`, label, exe))
		},
	}, nil
}
```

Create `internal/autostart/autostart_linux.go`:

```go
package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

// New returns an XDG-autostart-backed autostart.
func New() (Autostart, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return &fileAutostart{
		path: filepath.Join(dir, "autostart", "baobar.desktop"),
		exe:  os.Executable,
		render: func(exe string) []byte {
			return []byte(fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Baobar
Exec=%s
Terminal=false
X-GNOME-Autostart-enabled=true
`, exe))
		},
	}, nil
}
```

Create `internal/autostart/autostart_windows.go`:

```go
package autostart

import (
	"errors"
	"os"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const valueName = "Baobar"

type registryAutostart struct{}

// New returns a registry-backed autostart using the per-user Run key, which
// needs no elevation.
func New() (Autostart, error) { return &registryAutostart{}, nil }

func (registryAutostart) Enabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer k.Close()

	if _, _, err := k.GetStringValue(valueName); err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (registryAutostart) Enable() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(valueName, `"`+exe+`"`)
}

func (registryAutostart) Disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer k.Close()

	err = k.DeleteValue(valueName)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go get golang.org/x/sys/windows/registry
go test -race ./internal/autostart/ -v
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./internal/autostart/
```
Expected: tests PASS on macOS; the Windows build succeeds. The registry implementation cannot be exercised from macOS — say so in your report rather than claiming it is tested.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/autostart/
git add internal/autostart/ go.mod go.sum
git commit -m "feat(autostart): per-platform start-at-login behind one interface"
```

---

### Task 7: Wire it in, delete the shell-out

**Files:**
- Modify: `internal/tray/tray.go`, `cmd/baobar/main.go`
- Delete: `internal/login/` (entire directory)

**Interfaces:**
- Consumes: `authflow.OIDC`, `authflow.UserpassBrowser`, `authflow.OIDCConfig`, `authflow.UserpassBrowserConfig`, `authflow.UserpassConfig`, `authflow.ErrBusy`, `autostart.New`, `bao.WriteToken`, `config.Config` fields from Task 1.
- Produces: `tray.Options.StartAtLoginEnabled func() bool`, `tray.Options.ToggleStartAtLogin func(on bool) error`; `tray.Options.CLIAvailable` is removed.

- [ ] **Step 1: Delete the shell-out package**

```bash
git rm -r internal/login
```

- [ ] **Step 2: Update the tray**

In `internal/tray/tray.go`, remove the `CLIAvailable` field from `Options` and add:

```go
	// StartAtLoginEnabled reports the real on-disk state, re-read after every
	// toggle. ToggleStartAtLogin returns an error if the change did not happen,
	// so the checkbox can stay honest rather than claiming a state it failed to
	// reach.
	StartAtLoginEnabled func() bool
	ToggleStartAtLogin  func(on bool) error
```

Delete the whole `if !o.CLIAvailable { ... }` block that disabled and relabelled the login items.

Add the checkbox after the `mRefresh` item and before `mQuit`:

```go
	mStartAtLogin := systray.AddMenuItemCheckbox("Start at login", "Launch Baobar when you log in",
		o.StartAtLoginEnabled != nil && o.StartAtLoginEnabled())
```

Add `startAtLogin *systray.MenuItem` to the `menuItems` struct and
`startAtLogin: mStartAtLogin,` to the struct literal passed to `uiLoop`. Then handle its
clicks in `uiLoop`'s select, alongside the existing cases:

```go
		case <-m.startAtLogin.ClickedCh:
			want := !m.startAtLogin.Checked()
			if err := o.ToggleStartAtLogin(want); err != nil {
				o.alert("Start at login", "Could not change the setting.")
			}
			// Reflect what is actually on disk, not what was asked for.
			if o.StartAtLoginEnabled() {
				m.startAtLogin.Check()
			} else {
				m.startAtLogin.Uncheck()
			}
```

The login cases already run in their own goroutine from M1; leave that shape and disable
both items while a flow is live:

```go
		case <-m.userpass.ClickedCh:
			m.userpass.Disable()
			m.oidc.Disable()
			go func() {
				_ = o.Login("userpass")
				m.userpass.Enable()
				m.oidc.Enable()
				kick()
			}()
```

Do the same for `m.oidc.ClickedCh` with `o.Login("oidc")`.

- [ ] **Step 3: Update main**

In `cmd/baobar/main.go`, replace the `login` import with `authflow` and `autostart`, drop
`CLIAvailable`, and wire the flows:

```go
	starter, autostartErr := autostart.New()

	tray.Run(tray.Options{
		Addr:   cfg.Addr,
		Status: poller.Status,
		StartAtLoginEnabled: func() bool {
			if autostartErr != nil {
				return false
			}
			on, err := starter.Enabled()
			return err == nil && on
		},
		ToggleStartAtLogin: func(on bool) error {
			if autostartErr != nil {
				return autostartErr
			}
			if on {
				return starter.Enable()
			}
			return starter.Disable()
		},
		Login: func(method string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()

			var token string
			var err error
			switch method {
			case "oidc":
				token, err = authflow.OIDC(ctx, authflow.OIDCConfig{
					Addr: cfg.Addr, Mount: cfg.OIDCMount, Role: cfg.OIDCRole,
					CallbackPort: cfg.CallbackPort, OpenURL: openURL,
				})
			default:
				token, err = authflow.UserpassBrowser(ctx, authflow.UserpassBrowserConfig{
					Userpass:        authflow.UserpassConfig{Addr: cfg.Addr, Mount: cfg.UserpassMount},
					DefaultUsername: cfg.Username,
					OpenURL:         openURL,
				})
			}
			if err != nil {
				if errors.Is(err, authflow.ErrBusy) {
					notify.Send("OpenBao", "A login is already in progress.")
				} else {
					notify.Send("OpenBao", "Login did not complete.")
				}
				return err
			}
			if err := bao.WriteToken(tokenPath, token); err != nil {
				notify.Send("OpenBao", "Signed in, but the token could not be saved.")
				return err
			}
			poller.Force()
			return nil
		},
		// ... the remaining options are unchanged
	})
```

Note `poller.Force()` is called here, on the login goroutine. That is the racing call the
M1 review found — but `Poller`'s throttle state is mutex-guarded now, so it is safe.

- [ ] **Step 4: Verify**

```bash
gofmt -l .
go build ./... && go vet ./...
go test -race ./... -count=1
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui" -o /tmp/baobar.exe ./cmd/baobar
GOOS=linux GOARCH=amd64 go build -o /tmp/baobar-linux ./cmd/baobar
grep -rn "bao login\|CLIAvailable\|osascript\|x-terminal-emulator" --include=*.go . || echo "no shell-out remains"
```
Expected: all clean; the grep finds nothing.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat: log in without a terminal or the bao CLI, add start-at-login"
```

---

### Task 8: Documentation

**Files:**
- Modify: `README.md`, `docs/superpowers/specs/2026-08-13-baobar-design.md`

**Interfaces:** none.

- [ ] **Step 1: Update the README**

Make these changes, and check every sentence for claims that are no longer true:

1. The intro says login "opens a terminal" — it no longer does. Say the login items open your **browser**.
2. The Status section says the app has been run once and lists outstanding checks. Add that M2 login flows have unit tests but have **not** been exercised against a live server, if that is still true when you finish.
3. Add the five new settings to the configuration table: `oidc_mount` / `BAOBAR_OIDC_MOUNT` (default `oidc`), `oidc_role` / `BAOBAR_OIDC_ROLE` (default empty), `userpass_mount` / `BAOBAR_USERPASS_MOUNT` (default `userpass`), `callback_port` / `BAOBAR_CALLBACK_PORT` (default `8250`), `username` / `BAOBAR_USERNAME` (default empty, prefills the form only).
4. State plainly that `callback_port` must match the OIDC role's allowed redirect URI.
5. Document "Start at login" and where each platform stores its entry.
6. Remove any statement that Baobar needs or uses the `bao` CLI. It does not, for anything.

- [ ] **Step 2: Update the M1 design doc**

In `docs/superpowers/specs/2026-08-13-baobar-design.md`, the "M1 is a *partial* Windows win" passage says login is a dead end without the CLI. Add one sentence noting that M2 resolved it, and link to the M2 spec.

- [ ] **Step 3: Verify no stale claims**

```bash
grep -rn "terminal\|bao CLI\|command not found" README.md docs/superpowers/specs/*.md
```
Read each hit and confirm it is either historical context that is clearly marked as such, or has been corrected.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/
git commit -m "docs: browser-based login, start-at-login, and the new settings"
```

---

## Definition of done for M2

- `go test -race ./...` passes; `go vet ./...` and `gofmt -l .` are clean.
- No `.go` file references the `bao` CLI, a terminal, `osascript`, or `x-terminal-emulator`.
- `internal/login` no longer exists.
- Windows and Linux binaries cross-compile.
- Manual, against a live server: SSO login completes in the browser with no terminal and
  Baobar picks up the new token within a second or two; userpass+TOTP does the same; a
  wrong passcode re-prompts; "Start at login" survives a logout/login cycle; and the
  `~/Library/LaunchAgents` plist appears and disappears with the checkbox.
- The audit-log throttle still holds after a login (the forced poll is one request, not a
  new polling cadence).
