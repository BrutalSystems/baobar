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
	// serve() is what closes the listener; without it the socket leaks for the
	// life of the test binary.
	go s.finish("", nil)
	defer s.serve()

	if !strings.HasPrefix(s.baseURL(), "http://127.0.0.1:") {
		t.Errorf("baseURL = %q, want a loopback address", s.baseURL())
	}
}

// A request arriving with a foreign Host is either a cross-origin POST or DNS
// rebinding. These routes take a password; they refuse both.
func TestSessionRejectsAForeignHost(t *testing.T) {
	s, err := newSession(0, time.Minute)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	var reached bool
	s.handle("/x", func(w http.ResponseWriter, r *http.Request) { reached = true })
	go func() {
		req, _ := http.NewRequest(http.MethodGet, s.baseURL()+"/x", nil)
		req.Host = "evil.example.com"
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want 403", resp.StatusCode)
			}
			resp.Body.Close()
		}
		s.finish("done", nil)
	}()
	s.serve()

	if reached {
		t.Error("handler ran for a request with a foreign Host")
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
