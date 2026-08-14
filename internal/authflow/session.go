// Package authflow acquires an OpenBao token by driving the system browser
// through a login flow and catching the result on a short-lived loopback
// listener. It never invokes the bao CLI and never opens a terminal.
package authflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrBusy means another login flow is already running.
	ErrBusy = errors.New("a login is already in progress")
	// ErrTimeout means the browser never came back in time.
	ErrTimeout = errors.New("login timed out")
)

// DefaultTimeout bounds how long a listener may stay open.
const DefaultTimeout = 5 * time.Minute

var inFlight atomic.Bool

// acquire reserves the single login slot, returning false if one is taken.
func acquire() bool { return inFlight.CompareAndSwap(false, true) }

func release() { inFlight.Store(false) }

// nonce returns 32 random bytes, hex encoded.
func nonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// sanitize strips the request URL out of a transport error. Go's *url.Error
// embeds the full URL in Error(), and our request URLs carry an
// authorization code, a client_nonce, and a username — none of which may
// appear in an error string. The underlying cause is preserved.
func sanitize(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

type outcome struct {
	token string
	err   error
}

// session is one browser round-trip: a loopback listener serving a handful of
// routes until something calls finish, or the timeout expires.
type session struct {
	ln      net.Listener
	mux     *http.ServeMux
	srv     *http.Server
	done    chan outcome
	once    sync.Once
	timeout time.Duration
}

// newSession binds 127.0.0.1 on port (0 for any free port).
func newSession(port int, timeout time.Duration) (*session, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("listen on 127.0.0.1:%d: %w", port, err)
	}
	mux := http.NewServeMux()
	return &session{
		ln:      ln,
		mux:     mux,
		srv:     &http.Server{Handler: guard(ln.Addr().String(), mux)},
		done:    make(chan outcome, 1),
		timeout: timeout,
	}, nil
}

// guard refuses any request whose Host does not name our own listener,
// address and port. That defeats DNS rebinding: a public hostname that
// resolves to 127.0.0.1 still can't present our Host, since it doesn't
// control what the attacker's DNS record is named.
//
// It is NOT a CSRF defence. A browser always sends the correct Host for the
// connection it opened, so this check cannot stop a cross-origin form POST —
// and it must not try to reject cross-site requests wholesale, because an
// OIDC provider's redirect back to us IS a cross-site navigation. Any route
// that accepts credentials has to protect itself (see Task 5: an unguessable
// nonce in the path plus a same-origin Sec-Fetch-Site check on the POST).
func guard(addr string, h http.Handler) http.Handler {
	_, wantPort, _ := net.SplitHostPort(addr)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, err := net.SplitHostPort(r.Host)
		if err != nil {
			host, port = r.Host, ""
		}
		if (host != "127.0.0.1" && host != "localhost") || port != wantPort {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	})
}

func (s *session) baseURL() string {
	return "http://" + s.ln.Addr().String()
}

func (s *session) handle(pattern string, h http.HandlerFunc) {
	s.mux.HandleFunc(pattern, h)
}

// finish records the flow's result. Only the first call counts: a browser may
// hit the callback more than once.
func (s *session) finish(token string, err error) {
	s.once.Do(func() { s.done <- outcome{token: token, err: err} })
}

// serve runs the listener until finish, ctx is cancelled, or the timeout
// expires, then shuts it down. A cancelled ctx (e.g. the caller quitting or
// abandoning the flow) must not leave the listener open for the rest of the
// timeout — callers that wire this into a longer-lived context depend on a
// prompt return here.
func (s *session) serve(ctx context.Context) (string, error) {
	go s.srv.Serve(s.ln)

	timer := time.NewTimer(s.timeout)
	defer timer.Stop()

	var out outcome
	select {
	case out = <-s.done:
	case <-timer.C:
		out = outcome{err: ErrTimeout}
	case <-ctx.Done():
		out = outcome{err: ctx.Err()}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(shutdownCtx)

	return out.token, out.err
}

// writePage renders the minimal end-of-flow page shown in the browser. It never
// contains a token, an identity, or a server address. title and message are
// escaped before being written: message in particular is where later tasks
// will put an error string echoed from an identity provider or OpenBao.
func writePage(w http.ResponseWriter, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	safeTitle := html.EscapeString(title)
	safeMessage := html.EscapeString(message)
	fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>%s</title>"+
		"<body style=\"font:16px system-ui;padding:3rem;max-width:32rem;margin:auto\">"+
		"<h1 style=\"font-size:1.25rem\">%s</h1><p>%s</p>", safeTitle, safeTitle, safeMessage)
}
