package authflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBao stands in for OpenBao's OIDC endpoints. It remembers the
// client_nonce presented to auth_url and requires the callback exchange to
// present the identical value: the nonce exists to bind the two requests
// together, and a regression that regenerates it for the exchange would
// otherwise pass unnoticed.
func fakeBao(t *testing.T, authURLFor func(redirect string) string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	var authNonce string
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
			mu.Lock()
			authNonce = body.ClientNonce
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"auth_url": authURLFor(body.RedirectURI)},
			})
		case strings.HasSuffix(r.URL.Path, "/oidc/callback"):
			q := r.URL.Query()
			if q.Get("code") == "" || q.Get("state") == "" || q.Get("client_nonce") == "" {
				t.Errorf("callback exchange missing params: %v", q)
			}
			mu.Lock()
			want := authNonce
			mu.Unlock()
			if got := q.Get("client_nonce"); got != want {
				t.Errorf("callback client_nonce = %q, want %q (must match the auth_url request)", got, want)
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

// State is validated by OpenBao, not by us: we forward whatever the provider
// sent. A mismatch must surface as a failed login rather than a hang or a
// bogus token.
func TestOIDCMismatchedStateIsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oidc/auth_url"):
			var body struct {
				RedirectURI string `json:"redirect_uri"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"auth_url": body.RedirectURI + "?code=abc&state=wrong"},
			})
		default: // OpenBao rejects the exchange
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"errors":["state is invalid or expired"]}`))
		}
	}))
	defer srv.Close()

	cfg := OIDCConfig{
		Addr: srv.URL, Mount: "oidc", CallbackPort: 0, Timeout: 5 * time.Second,
		OpenURL: func(u string) error { go http.Get(u); return nil },
	}

	if _, err := OIDC(context.Background(), cfg); err == nil {
		t.Fatal("expected an error when OpenBao rejects the state")
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

// A 302 on the callback exchange must never be followed. Go sends a Referer
// header on the resent request carrying the current URL — which for this
// exchange includes the authorization code, state, and client_nonce as query
// parameters — to whatever host the redirect names. This is the same leak
// class fixed twice already in this milestone (Task 3's nonce-guarded form,
// the Sec-Fetch-Site check), arriving here through net/http's default
// redirect-following instead.
func TestOIDCExchangeDoesNotFollowRedirect(t *testing.T) {
	var recordingHits int
	recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordingHits++
	}))
	defer recorder.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oidc/auth_url"):
			var body struct {
				RedirectURI string `json:"redirect_uri"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"auth_url": body.RedirectURI + "?code=abc&state=xyz"},
			})
		case strings.HasSuffix(r.URL.Path, "/oidc/callback"):
			http.Redirect(w, r, recorder.URL+"/steal", http.StatusFound)
		}
	}))
	defer srv.Close()

	cfg := OIDCConfig{
		Addr: srv.URL, Mount: "oidc", CallbackPort: 0, Timeout: 5 * time.Second,
		OpenURL: func(u string) error { go http.Get(u); return nil },
	}

	if _, err := OIDC(context.Background(), cfg); err == nil {
		t.Fatal("expected an error when the callback exchange redirects instead of answering")
	}
	if recordingHits != 0 {
		t.Errorf("recording server received %d requests, want 0 — the auth code/nonce must never follow a redirect", recordingHits)
	}
}

// The equivalent of TestUserpassBrowserReturnsPromptlyOnContextCancellation
// for the OIDC flow: a cancelled context must unblock OIDC promptly rather
// than waiting out cfg.Timeout, since the tray wires a cancel into a login
// goroutine it may need to abandon. Unlike UserpassBrowser, OIDC's first step
// (authURL) talks to the server before the browser round-trip begins, so this
// uses a working fake OpenBao and simply never visits the auth URL (OpenURL
// does nothing) — the blocking point under test is the same s.serve(ctx) both
// flows share.
func TestOIDCReturnsPromptlyOnContextCancellation(t *testing.T) {
	bao := fakeBao(t, func(redirect string) string { return redirect + "?code=abc&state=xyz" })
	defer bao.Close()

	cfg := OIDCConfig{
		Addr: bao.URL, Mount: "oidc", CallbackPort: 0, Timeout: time.Minute,
		OpenURL: func(string) error { return nil }, // browser never comes back
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := OIDC(ctx, cfg)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("OIDC took %v after cancellation, want well under the 1-minute timeout", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// A transport failure must not leak the authorization code or client_nonce.
// Go's *url.Error embeds the full request URL in Error(), and the callback
// exchange carries both as query parameters.
func TestOIDCTransportErrorDoesNotLeakCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // closed immediately: any request against it fails at the transport layer

	cfg := OIDCConfig{Addr: srv.URL, Mount: "oidc"}
	const secretCode = "SUPER-SECRET-AUTH-CODE"
	const secretNonce = "SUPER-SECRET-CLIENT-NONCE"

	_, err := cfg.exchange(context.Background(), "xyz", secretCode, secretNonce)
	if err == nil {
		t.Fatal("expected a transport error against a closed server")
	}
	if strings.Contains(err.Error(), secretCode) {
		t.Errorf("err leaked the authorization code: %v", err)
	}
	if strings.Contains(err.Error(), secretNonce) {
		t.Errorf("err leaked the client_nonce: %v", err)
	}
}
