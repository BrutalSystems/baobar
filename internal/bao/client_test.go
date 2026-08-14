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
