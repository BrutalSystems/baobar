package authflow

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The real failure this exists for: the OIDC role does not permit the redirect
// URI Baobar sends. OpenBao names the problem exactly; the user must see it.
func TestOIDCSurfacesAnUnauthorizedRedirectURI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"errors":["unauthorized redirect_uri: http://127.0.0.1:8250/oidc/callback"]}`))
	}))
	defer srv.Close()

	_, err := OIDC(context.Background(), OIDCConfig{
		Addr: srv.URL, Mount: "oidc", CallbackPort: 0, Timeout: time.Second,
		OpenURL: func(string) error { return nil },
	})
	if err == nil {
		t.Fatal("expected an error")
	}

	var p *ConfigProblem
	if !errors.As(err, &p) {
		t.Fatalf("err = %v (%T), want a *ConfigProblem so the caller can show it", err, err)
	}
	if !strings.Contains(p.Detail, "unauthorized redirect_uri") {
		t.Errorf("Detail = %q, want it to name the redirect URI problem", p.Detail)
	}
	if p.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", p.Status)
	}
}

// A server that returns no errors array still yields a usable ConfigProblem
// rather than a nil detail the caller might render as an empty message.
func TestConfigProblemWithoutAnErrorsArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`not json at all`))
	}))
	defer srv.Close()

	_, err := OIDC(context.Background(), OIDCConfig{
		Addr: srv.URL, Mount: "oidc", CallbackPort: 0, Timeout: time.Second,
		OpenURL: func(string) error { return nil },
	})

	var p *ConfigProblem
	if !errors.As(err, &p) {
		t.Fatalf("err = %v, want a *ConfigProblem", err)
	}
	if p.Error() == "" {
		t.Error("Error() is empty; a notification would show nothing")
	}
	if strings.Contains(p.Detail, "not json") {
		t.Errorf("Detail = %q — a raw body must never be surfaced", p.Detail)
	}
}

// An occupied callback port is the other thing the user can fix, and it cannot
// be worked around silently: the port must match the role's redirect URI.
func TestOIDCSurfacesAnOccupiedCallbackPort(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port

	_, err = OIDC(context.Background(), OIDCConfig{
		Addr: "https://bao.example.com", Mount: "oidc", CallbackPort: port,
		Timeout: time.Second, OpenURL: func(string) error { return nil },
	})

	var p *ConfigProblem
	if !errors.As(err, &p) {
		t.Fatalf("err = %v, want a *ConfigProblem naming the port", err)
	}
	if !strings.Contains(p.Detail, "port") {
		t.Errorf("Detail = %q, want it to mention the port", p.Detail)
	}
}

// A body far larger than any real error response must not be repeated back
// wholesale into a desktop notification.
func TestConfigProblemDetailIsBounded(t *testing.T) {
	huge := strings.Repeat("A", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"errors":["` + huge + `"]}`))
	}))
	defer srv.Close()

	_, err := OIDC(context.Background(), OIDCConfig{
		Addr: srv.URL, Mount: "oidc", CallbackPort: 0, Timeout: time.Second,
		OpenURL: func(string) error { return nil },
	})

	var p *ConfigProblem
	if !errors.As(err, &p) {
		t.Fatalf("err = %v, want a *ConfigProblem", err)
	}
	if len(p.Detail) > maxDetail+10 {
		t.Errorf("Detail is %d chars, want it bounded near %d", len(p.Detail), maxDetail)
	}
}

// The OIDC role's user_claim names a claim the identity provider does not
// emit — the single most common way a working-on-paper SSO setup fails. OpenBao
// says exactly which claim is missing; flattening that to a status code left the
// user with "Login did not complete" and nothing to act on.
func TestOIDCSurfacesAMissingClaimFromTheCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oidc/auth_url") {
			var body struct {
				RedirectURI string `json:"redirect_uri"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"auth_url": body.RedirectURI + "?code=abc&state=xyz"},
			})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"errors":["OpenBao login failed. claim \"email\" not found in token"]}`))
	}))
	defer srv.Close()

	_, err := OIDC(context.Background(), OIDCConfig{
		Addr: srv.URL, Mount: "oidc", CallbackPort: 0, Timeout: 5 * time.Second,
		OpenURL: func(u string) error { go http.Get(u); return nil },
	})
	if err == nil {
		t.Fatal("expected an error")
	}

	var p *ConfigProblem
	if !errors.As(err, &p) {
		t.Fatalf("err = %v (%T), want a *ConfigProblem so the tray can show the reason", err, err)
	}
	if !strings.Contains(p.Detail, `claim "email" not found`) {
		t.Errorf("Detail = %q, want it to name the missing claim", p.Detail)
	}
	if p.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", p.Status)
	}
}

// Userpass carries a username and password in the request body, and its
// failures are credentials-wrong rather than configuration-wrong. It stays
// generic: unlike the OIDC callback above, there is no server-authored config
// error worth surfacing, and its body is not something to render.
func TestExchangeFailureIsNotAConfigProblem(t *testing.T) {
	cfg := UserpassConfig{Addr: "https://bao.example.com", Mount: "userpass"}
	_, _, err := cfg.login(context.Background(), "userpass-dev", "pw")
	if err == nil {
		t.Skip("no error to classify")
	}
	var p *ConfigProblem
	if errors.As(err, &p) {
		t.Errorf("a userpass failure surfaced as a ConfigProblem (%q); credential paths stay generic", p.Detail)
	}
}
