package authflow

import (
	"context"
	"encoding/json"
	"errors"
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

// The Sec-Fetch-Site check must run before any form value is read — not just
// before OpenBao is contacted. This body would fail to parse (%zz is not a
// valid percent-escape): if the check ever moved after r.ParseForm(), the
// malformed body would make ParseForm fail first and the handler would
// render the "Could not read the form" page (200) instead of rejecting the
// cross-site request outright (403).
func TestUserpassBrowserRejectsCrossSitePOSTBeforeParsingBody(t *testing.T) {
	bao := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				req, err := http.NewRequest(http.MethodPost, u, strings.NewReader("username=%zz"))
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
					t.Errorf("status = %d, want 403 (the Sec-Fetch-Site check must run before the body is parsed)", resp.StatusCode)
				}
			}()
			return nil
		},
	}

	UserpassBrowser(context.Background(), cfg)
	<-done
}

// Sec-Fetch-Site: none is what a browser sends for a direct, user-driven
// navigation (typing the URL, a bookmark, a plain redirect from the tray) —
// it must be accepted, not just an absent header or "same-origin".
func TestUserpassBrowserAcceptsSecFetchSiteNone(t *testing.T) {
	bao := mfaBao(t)
	defer bao.Close()

	done := make(chan struct{})
	cfg := UserpassBrowserConfig{
		Userpass: UserpassConfig{Addr: bao.URL, Mount: "userpass"},
		Timeout:  5 * time.Second,
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
				req.Header.Set("Sec-Fetch-Site", "none")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Errorf("POST form: %v", err)
					return
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Errorf("status = %d, want 200 (Sec-Fetch-Site: none is a direct navigation)", resp.StatusCode)
				}
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

// The route must be an exact match, not a subtree: if it were ever registered
// as a prefix (e.g. a trailing slash turning it into a ServeMux subtree), a
// guessed suffix appended to the real nonce path would also reach the
// handler.
func TestUserpassBrowserRouteIsExactMatch(t *testing.T) {
	bao := mfaBao(t)
	defer bao.Close()

	got := make(chan int, 1)
	cfg := UserpassBrowserConfig{
		Userpass: UserpassConfig{Addr: bao.URL, Mount: "userpass"},
		Timeout:  300 * time.Millisecond,
		OpenURL: func(u string) error {
			go func() {
				resp, err := http.Get(u + "/extra")
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
			t.Errorf("suffixed path returned %d, want 404", code)
		}
	case <-time.After(time.Second):
		t.Error("no response observed")
	}
}

// login() rejects a username containing '/', '\', '?', '#', or control
// characters, but NOT '"' or '<' — so a bad-credentials re-render of the
// username is the last line of defence against XSS on the loopback origin.
// That defence is html/template's auto-escaping: this test would catch a
// regression to text/template or fmt.Fprintf, either of which would make this
// live script injection into a page served on 127.0.0.1.
func TestUserpassBrowserEscapesUsernameInReRender(t *testing.T) {
	bao := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer bao.Close()

	const xssUsername = `x" onfocus="alert(1)<script>bad</script>`

	done := make(chan struct{})
	cfg := UserpassBrowserConfig{
		Userpass: UserpassConfig{Addr: bao.URL, Mount: "userpass"},
		// The flow never succeeds (bad credentials only), so it always runs
		// out the clock — keep the timeout short so the test stays fast.
		Timeout: 300 * time.Millisecond,
		OpenURL: func(u string) error {
			go func() {
				defer close(done)
				resp, err := http.PostForm(u, url.Values{
					"username": {xssUsername}, "password": {"wrongpw"},
				})
				if err != nil {
					t.Errorf("POST form: %v", err)
					return
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				got := string(body)

				if !strings.Contains(got, "&#34;") {
					t.Error("re-rendered form did not escape the double quote as &#34;")
				}
				if !strings.Contains(got, "&lt;script&gt;") {
					t.Error("re-rendered form did not escape <script> as &lt;script&gt;")
				}
				if strings.Contains(got, `<script>bad</script>`) {
					t.Error("re-rendered form contains a raw, unescaped <script> tag")
				}
				if strings.Contains(got, `" onfocus="`) {
					t.Error("re-rendered form contains a raw, unescaped attribute breakout")
				}
			}()
			return nil
		},
	}

	UserpassBrowser(context.Background(), cfg)
	<-done
}

// Task 7 is the first real caller of (*session).serve with a cancellable
// context, and it drives that cancellation through the public entry point,
// not serve() directly. A cancelled context must unblock UserpassBrowser
// promptly rather than waiting out cfg.Timeout, since the tray wires a
// cancel into a login goroutine it may need to abandon.
func TestUserpassBrowserReturnsPromptlyOnContextCancellation(t *testing.T) {
	cfg := UserpassBrowserConfig{
		Userpass: UserpassConfig{Addr: "http://127.0.0.1:0", Mount: "userpass"},
		Timeout:  time.Minute,
		OpenURL:  func(string) error { return nil },
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := UserpassBrowser(ctx, cfg)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("UserpassBrowser took %v after cancellation, want well under the 1-minute timeout", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
