package authflow

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// post submits the setup form the way a browser would.
func postAddr(t *testing.T, formURL, addr string) string {
	t.Helper()
	resp, err := http.PostForm(formURL, url.Values{"addr": {addr}})
	if err != nil {
		t.Errorf("POST: %v", err)
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// First run with no address: Baobar serves a one-field form, the user types an
// address, and it is saved. Without this the only way to start the app is to
// hand-write TOML in a terminal — which is the one thing the product claims you
// do not have to do.
func TestSetupSavesTheAddressTheUserSubmits(t *testing.T) {
	var saved string
	done := make(chan struct{})

	got, err := Setup(context.Background(), SetupConfig{
		Timeout: 5 * time.Second,
		Save: func(addr string) error {
			saved = addr
			return nil
		},
		OpenURL: func(u string) error {
			go func() {
				defer close(done)
				if _, err := http.Get(u); err != nil {
					t.Errorf("GET form: %v", err)
					return
				}
				postAddr(t, u, "https://bao.example.com")
			}()
			return nil
		},
	})
	<-done

	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if got != "https://bao.example.com" {
		t.Errorf("Setup returned %q", got)
	}
	if saved != "https://bao.example.com" {
		t.Errorf("Save called with %q", saved)
	}
}

// A rejected address must send the user back to the form with the reason, not
// end the flow — otherwise a typo drops them back to a tray icon with no way in.
func TestSetupRedisplaysTheFormWhenSaveRejects(t *testing.T) {
	done := make(chan struct{})
	var attempts int

	got, err := Setup(context.Background(), SetupConfig{
		Timeout: 5 * time.Second,
		Save: func(addr string) error {
			attempts++
			if strings.Contains(addr, "example.com") {
				return nil
			}
			return errors.New("addr must be http or https")
		},
		OpenURL: func(u string) error {
			go func() {
				defer close(done)
				body := postAddr(t, u, "gopher://nope")
				if !strings.Contains(body, "http or https") {
					t.Errorf("rejection page does not explain why: %q", body)
				}
				postAddr(t, u, "https://bao.example.com")
			}()
			return nil
		},
	})
	<-done

	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if got != "https://bao.example.com" {
		t.Errorf("Setup returned %q after a retry", got)
	}
	if attempts != 2 {
		t.Errorf("Save called %d times, want 2 (one rejected, one accepted)", attempts)
	}
}
