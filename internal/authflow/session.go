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
	"syscall"
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
//
// ln is always present. extra holds additional loopback listeners bound to
// the same port on another address family — currently only newLoopbackSession
// populates it, with a single [::1] listener alongside ln's 127.0.0.1. host,
// when set, is the hostname baseURL() reports instead of ln's bare IP; it
// exists so a dual-stack session can hand out "localhost" — the form allow-
// listed by an OIDC role and identity provider — while still being backed by
// concrete IP listeners underneath.
type session struct {
	ln      net.Listener
	extra   []net.Listener
	host    string
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

// ipv6Unavailable reports whether an IPv6 bind failed because this host has
// no usable IPv6 — the only reason it is safe to continue with IPv4 alone.
// Everything else, including a port already held by another process, is
// fatal: proceeding would let whoever holds [::1] receive our callback.
// Enumerating the SAFE errors rather than the fatal ones matters because
// syscall.EADDRINUSE is a fabricated placeholder on Windows, where the real
// Winsock error is 10048 — an unknown-error default would fail open there.
func ipv6Unavailable(err error) bool {
	for _, safe := range []error{
		syscall.EAFNOSUPPORT,
		syscall.EADDRNOTAVAIL,
		syscall.EPROTONOSUPPORT,
		syscall.ENETUNREACH,
		syscall.Errno(10047), // WSAEAFNOSUPPORT
		syscall.Errno(10049), // WSAEADDRNOTAVAIL
	} {
		if errors.Is(err, safe) {
			return true
		}
	}
	return false
}

// newLoopbackSession binds port on BOTH 127.0.0.1 and [::1], and its
// baseURL() names the host "localhost" instead of an IP literal — the
// redirect URI form already allow-listed on the OpenBao OIDC role and (unlike
// a 127.0.0.1 literal, which some identity providers reject outright) on the
// identity provider's app registration too.
//
// Binding both loopback addresses closes the gap a single-stack listener
// would leave: on a dual-stack host "localhost" can resolve to ::1 before
// 127.0.0.1 (Windows prefers it), so if we only bound the v4 side, another
// local process already squatting [::1]:port could be the one to receive
// the OIDC callback — authorization code and all — instead of us. Whether
// that's what's happening is decided by ipv6Unavailable: only a recognised
// no-IPv6-here error is safe to proceed past. Everything else, including
// [::1]:port already being held, is fatal — handing the callback to
// whatever's sitting on the address we couldn't get is the exact outcome
// this function exists to prevent, so it fails closed by default rather
// than trying to enumerate every fatal case.
//
// port == 0 (any free port) retries the whole pair up to five times: the
// OS-picked v4 port can, rarely, already be held on [::1] by something
// unrelated to any squatting attempt, and a fresh port sidesteps that
// without weakening the fatal check itself. A caller-supplied fixed port
// (the OIDC callback's normal case) gets a single attempt, same as before —
// retrying a port the caller specifically asked for would just mask a real
// conflict.
func newLoopbackSession(port int, timeout time.Duration) (*session, error) {
	tries := 1
	if port == 0 {
		tries = 5
	}

	var err error
	for i := 0; i < tries; i++ {
		var s *session
		if s, err = bindLoopbackPair(port, timeout); err == nil {
			return s, nil
		}
	}
	return nil, err
}

// bindLoopbackPair is newLoopbackSession's single attempt: bind 127.0.0.1,
// then attempt [::1] on the same port. On any early return the listeners
// already opened are closed via session.close() — between here and the
// caller's serve(), nothing else owns them.
func bindLoopbackPair(port int, timeout time.Duration) (*session, error) {
	ln4, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("listen on 127.0.0.1:%d: %w", port, err)
	}
	_, p, _ := net.SplitHostPort(ln4.Addr().String())

	s := &session{
		ln:      ln4,
		host:    "localhost",
		done:    make(chan outcome, 1),
		timeout: timeout,
	}

	ln6, err := net.Listen("tcp", fmt.Sprintf("[::1]:%s", p))
	switch {
	case err == nil:
		s.extra = append(s.extra, ln6)
	case ipv6Unavailable(err):
		// No usable IPv6 on this host — proceed on IPv4 only. A host with
		// no IPv6 resolves "localhost" only to 127.0.0.1, so this is safe,
		// not a compromise.
	default:
		// Anything else — most importantly [::1]:port already being held —
		// is fatal. Do not guess; refuse.
		s.close()
		return nil, fmt.Errorf(
			"could not secure port %s on both loopback addresses (127.0.0.1 "+
				"and [::1]); refusing to leave the login callback reachable "+
				"to whatever is holding [::1]: %w", p, err)
	}

	mux := http.NewServeMux()
	s.mux = mux
	s.srv = &http.Server{Handler: guard(ln4.Addr().String(), mux)}
	return s, nil
}

// close closes every listener the session holds. It exists for early-return
// paths before serve() has run — once serve() is running, its own shutdown
// handles this instead.
func (s *session) close() {
	if s.ln != nil {
		s.ln.Close()
	}
	for _, ln := range s.extra {
		ln.Close()
	}
}

// guard refuses any request whose Host does not name our own listener,
// address and port. That defeats DNS rebinding: a public hostname that
// resolves to 127.0.0.1 (or ::1) still can't present our Host, since it
// doesn't control what the attacker's DNS record is named.
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
		if (host != "127.0.0.1" && host != "localhost" && host != "::1") || port != wantPort {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	})
}

func (s *session) baseURL() string {
	if s.host == "" {
		return "http://" + s.ln.Addr().String()
	}
	_, port, _ := net.SplitHostPort(s.ln.Addr().String())
	return "http://" + net.JoinHostPort(s.host, port)
}

func (s *session) handle(pattern string, h http.HandlerFunc) {
	s.mux.HandleFunc(pattern, h)
}

// finish records the flow's result. Only the first call counts: a browser may
// hit the callback more than once.
func (s *session) finish(token string, err error) {
	s.once.Do(func() { s.done <- outcome{token: token, err: err} })
}

// serve runs every listener (ln, plus any extra) until finish, ctx is
// cancelled, or the timeout expires, then shuts all of them down. A cancelled
// ctx (e.g. the caller quitting or abandoning the flow) must not leave a
// listener open for the rest of the timeout — callers that wire this into a
// longer-lived context depend on a prompt return here. http.Server.Shutdown
// closes every listener that was handed to this same *http.Server via Serve,
// so one Shutdown call is enough to take all of them down together.
func (s *session) serve(ctx context.Context) (string, error) {
	go s.srv.Serve(s.ln)
	for _, ln := range s.extra {
		go s.srv.Serve(ln)
	}

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
