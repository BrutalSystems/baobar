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

// A cross-site POST must be refused before any credential is read or sent to
// OpenBao. The session's Host guard cannot catch this (a cross-origin POST
// still carries our own Host), so the handler itself must check
// Sec-Fetch-Site.
func TestUserpassBrowserRejectsCrossSitePOST(t *testing.T) {
	var loginHit bool
	bao := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/login/") {
			loginHit = true
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer bao.Close()

	done := make(chan struct{})
	cfg := UserpassBrowserConfig{
		Userpass: UserpassConfig{Addr: bao.URL, Mount: "userpass"},
		Timeout:  300 * time.Millisecond,
		OpenURL: func(u string) error {
			go func() {
				defer close(done)
				req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(url.Values{
					"username": {"userpass-dev"}, "password": {"pw"}, "passcode": {"910201"},
				}.Encode()))
				if err != nil {
					t.Errorf("build request: %v", err)
					return
				}
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("Sec-Fetch-Site", "cross-site")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Errorf("POST form: %v", err)
					return
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusForbidden {
					t.Errorf("status = %d, want 403", resp.StatusCode)
				}
			}()
			return nil
		},
	}

	UserpassBrowser(context.Background(), cfg)
	<-done

	if loginHit {
		t.Error("cross-site POST reached OpenBao's login endpoint")
	}
}
