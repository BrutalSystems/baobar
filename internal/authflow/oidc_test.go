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
