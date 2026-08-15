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

// Ordinary usernames must survive url.PathEscape unchanged: '@' and '.' are
// legal path characters, so an OIDC-style email address must not be mangled.
func TestUserpassLoginEscapesOrdinaryUsernamesUnchanged(t *testing.T) {
	for _, username := range []string{"userpass-dev", "someone@example.com"} {
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.EscapedPath()
			json.NewEncoder(w).Encode(map[string]any{
				"auth": map[string]any{"client_token": "hvs.tok"},
			})
		}))

		cfg := UserpassConfig{Addr: srv.URL, Mount: "userpass"}
		if _, _, err := cfg.login(context.Background(), username, "pw"); err != nil {
			t.Fatalf("login(%q): %v", username, err)
		}
		want := "/v1/auth/userpass/login/" + username
		if gotPath != want {
			t.Errorf("username %q: path = %s, want %s", username, gotPath, want)
		}
		srv.Close()
	}
}

// A username is free text from a form. url.PathEscape alone would leave the
// request's safety depending on OpenBao's router not re-splitting a decoded
// "%2F" back into a path separator — unverified behaviour in someone else's
// router is not a defence. login must instead refuse outright, client-side,
// any username that could alter the request path, and it must never even
// dial the server for one: a rejected password and a rejected username look
// identical to the caller (ErrBadCredentials), so nothing sensitive escapes
// this process for a malformed value either.
func TestUserpassLoginRejectsUnsafeUsernames(t *testing.T) {
	unsafe := []string{
		"../../sys/mfa/validate",
		"a/b",
		`a\b`,
		"a?b",
		"a#b",
		"a\nb",
		"",
	}
	for _, username := range unsafe {
		var requests int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			json.NewEncoder(w).Encode(map[string]any{
				"auth": map[string]any{"client_token": "hvs.tok"},
			})
		}))

		cfg := UserpassConfig{Addr: srv.URL, Mount: "userpass"}
		token, ch, err := cfg.login(context.Background(), username, "pw")
		if !errors.Is(err, ErrBadCredentials) {
			t.Errorf("username %q: err = %v, want ErrBadCredentials", username, err)
		}
		if ch != nil {
			t.Errorf("username %q: challenge = %+v, want nil", username, ch)
		}
		if token != "" {
			t.Errorf("username %q: token = %q, want empty", username, token)
		}
		if requests != 0 {
			t.Errorf("username %q: server received %d requests, want 0 — login must reject before dialing out", username, requests)
		}
		srv.Close()
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

// With several enforcements configured, the chosen method must not vary run to
// run — map iteration order in Go is deliberately randomised.
func TestUserpassChallengeSelectionIsDeterministic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"auth":{"mfa_requirement":{
			"mfa_request_id":"req-3",
			"mfa_constraints":{
				"b_enforcement":{"any":[{"type":"totp","id":"uuid-b","uses_passcode":true}]},
				"a_enforcement":{"any":[{"type":"totp","id":"uuid-a","uses_passcode":true}]}}}}}`))
	}))
	defer srv.Close()

	cfg := UserpassConfig{Addr: srv.URL, Mount: "userpass"}
	for i := 0; i < 20; i++ {
		_, ch, err := cfg.login(context.Background(), "userpass-dev", "pw")
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		if ch.MethodKey != "uuid-a" {
			t.Fatalf("run %d picked %q, want uuid-a every time (sorted by enforcement name)", i, ch.MethodKey)
		}
	}
}

// A method that doesn't use a passcode (e.g. push/Duo) must never be offered
// as a TOTP challenge. With no passcode-capable method present, login must
// fail loudly rather than hand back a challenge nothing can satisfy.
func TestUserpassChallengeSkipsMethodsWithoutPasscode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"auth":{"mfa_requirement":{
			"mfa_request_id":"req-4",
			"mfa_constraints":{"enforcement":{"any":[
				{"type":"duo","id":"uuid-duo","uses_passcode":false}]}}}}}`))
	}))
	defer srv.Close()

	cfg := UserpassConfig{Addr: srv.URL, Mount: "userpass"}
	token, ch, err := cfg.login(context.Background(), "userpass-dev", "pw")
	if err == nil {
		t.Fatalf("login: got nil error, want an error naming the missing passcode method")
	}
	if ch != nil {
		t.Fatalf("challenge = %+v, want nil when no method accepts a passcode", ch)
	}
	if token != "" {
		t.Fatalf("token = %q, want empty", token)
	}
}

// When several methods are offered and the first offered doesn't use a
// passcode, the loop must keep looking rather than stopping at the first
// (unusable) entry.
func TestUserpassChallengeSkipsNonPasscodeMethodBeforeValidOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"auth":{"mfa_requirement":{
			"mfa_request_id":"req-5",
			"mfa_constraints":{"enforcement":{"any":[
				{"type":"duo","id":"uuid-duo","uses_passcode":false},
				{"type":"totp","id":"uuid-totp","uses_passcode":true}]}}}}}`))
	}))
	defer srv.Close()

	cfg := UserpassConfig{Addr: srv.URL, Mount: "userpass"}
	_, ch, err := cfg.login(context.Background(), "userpass-dev", "pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if ch == nil || ch.MethodKey != "uuid-totp" {
		t.Fatalf("challenge = %+v, want the passcode-capable method uuid-totp", ch)
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

// A 307 redirect from the configured address must never be followed: Go
// resends a POST body verbatim on a 307, so following one here would send the
// password to whatever host the redirect names. This is the third instance
// of that leak class in this milestone (the first two were the login form's
// own defences); this one arrives through net/http's default redirect
// following, which CheckRedirect on the client must refuse.
func TestUserpassLoginDoesNotFollowRedirect(t *testing.T) {
	var recordingHits int
	recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordingHits++
	}))
	defer recorder.Close()

	bao := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, recorder.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer bao.Close()

	cfg := UserpassConfig{Addr: bao.URL, Mount: "userpass"}
	_, _, err := cfg.login(context.Background(), "userpass-dev", "hunter2")
	if err == nil {
		t.Fatal("expected an error when the login endpoint redirects instead of answering")
	}
	if recordingHits != 0 {
		t.Errorf("recording server received %d requests, want 0 — the password must never follow a redirect", recordingHits)
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
