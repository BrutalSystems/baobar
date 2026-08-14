package authflow

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
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

// A foreign Host is DNS rebinding: a public hostname pointed at 127.0.0.1.
// The guard refuses it before the handler runs. (It is not a CSRF defence —
// see the doc comment on guard in session.go.)
func TestSessionRejectsAForeignHost(t *testing.T) {
	cases := []string{
		"evil.example.com",
		"127.0.0.1.evil.com",
		"localhost.evil.com",
		"127.0.0.2",
	}
	for _, host := range cases {
		t.Run(host, func(t *testing.T) {
			s, err := newSession(0, time.Minute)
			if err != nil {
				t.Fatalf("newSession: %v", err)
			}
			var reached atomic.Bool
			s.handle("/x", func(w http.ResponseWriter, r *http.Request) { reached.Store(true) })

			_, port, err := net.SplitHostPort(s.ln.Addr().String())
			if err != nil {
				t.Fatalf("SplitHostPort: %v", err)
			}
			go func() {
				req, _ := http.NewRequest(http.MethodGet, s.baseURL()+"/x", nil)
				req.Host = net.JoinHostPort(host, port)
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

			if reached.Load() {
				t.Error("handler ran for a request with a foreign Host")
			}
		})
	}
}

// The Host guard must also refuse a request naming our own host but the
// wrong port: without that check, anything on the loopback interface (not
// just our listener) would pass.
func TestSessionRejectsRightHostWrongPort(t *testing.T) {
	s, err := newSession(0, time.Minute)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	var reached atomic.Bool
	s.handle("/x", func(w http.ResponseWriter, r *http.Request) { reached.Store(true) })

	go func() {
		req, _ := http.NewRequest(http.MethodGet, s.baseURL()+"/x", nil)
		req.Host = "127.0.0.1:1" // deliberately not our listener's port
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

	if reached.Load() {
		t.Error("handler ran for a request naming the right host but the wrong port")
	}
}

// Rejecting a foreign Host must not accidentally reject our own — the guard
// deliberately accepts both 127.0.0.1 and localhost, since OpenBao's
// conventional OIDC redirect URI is http://localhost:8250/oidc/callback.
func TestSessionAcceptsOurOwnHost(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost"} {
		t.Run(host, func(t *testing.T) {
			s, err := newSession(0, time.Minute)
			if err != nil {
				t.Fatalf("newSession: %v", err)
			}
			var reached atomic.Bool
			s.handle("/x", func(w http.ResponseWriter, r *http.Request) {
				reached.Store(true)
				s.finish("ok", nil)
			})

			_, port, err := net.SplitHostPort(s.ln.Addr().String())
			if err != nil {
				t.Fatalf("SplitHostPort: %v", err)
			}
			go func() {
				req, _ := http.NewRequest(http.MethodGet, s.baseURL()+"/x", nil)
				req.Host = net.JoinHostPort(host, port)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					s.finish("", err)
					return
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Errorf("status = %d, want 200", resp.StatusCode)
				}
			}()
			if _, err := s.serve(); err != nil {
				t.Fatalf("serve: %v", err)
			}
			if !reached.Load() {
				t.Errorf("handler did not run for Host %q", host)
			}
		})
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

// A listener left open after serve() returns is exactly the standing local
// endpoint the timeout is meant to prevent: once serve() returns, the port
// must actually stop accepting connections.
func TestSessionShutsDownOnTimeout(t *testing.T) {
	s, err := newSession(0, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	addr := s.ln.Addr().String()

	if _, err := s.serve(); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}

	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		conn.Close()
		t.Error("listener still accepting connections after serve() returned")
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

// finish must stay idempotent under real concurrency, not just sequential
// calls from one goroutine: a plain bool guard (instead of sync.Once) would
// be a data race here, and -race must catch it.
func TestSessionFinishIsIdempotentConcurrently(t *testing.T) {
	s, err := newSession(0, time.Minute)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}

	const n = 50
	for i := 0; i < n; i++ {
		i := i
		go s.finish(fmt.Sprintf("token-%d", i), nil)
	}

	token, err := s.serve()
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	if !strings.HasPrefix(token, "token-") {
		t.Errorf("token = %q, want one of the concurrently-finished values", token)
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
