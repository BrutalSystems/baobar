package bao

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Lookuper is the slice of Client the poller needs, so tests can fake it.
type Lookuper interface {
	LookupSelf(ctx context.Context, token string, now time.Time) (Info, error)
}

// Poller turns the token file, the cache, and the server into a single Status.
//
// It calls the server at most once per Recheck window — unconditionally, not
// only when a usable cache happens to exist. Do not "fix" a stale-looking
// countdown by shortening Recheck — the countdown is recomputed locally on
// every call and is never stale. See the design doc's audit-log invariant.
//
// Status blocks on the network when it decides to call the server, so callers
// must run it off any UI goroutine (see internal/tray).
//
// Status itself is normally called from a single goroutine — the tray's poll
// goroutine (see internal/tray.onReadyNormal) — but Force is deliberately
// reachable from other goroutines (a menu click, a logout/login attempt
// finishing on its own goroutine). The throttle state below is therefore
// guarded by mu rather than left to that single-caller convention: a lost
// update to forceNext or lastAttempt under concurrent access would mean
// either a stuck stale Status forever, or the exact unthrottled-polling bug
// this throttle exists to prevent — silently, without a race detector to
// catch it in production. The mutex is never held across the network call in
// Status: it's taken to read the throttle state and decide, released, then
// re-taken only to record the outcome.
type Poller struct {
	Client    Lookuper
	TokenPath string
	CachePath string
	// Addr identifies the server Client talks to. It is recorded in the cache
	// so an entry from a different server is never served; without it, changing
	// VAULT_ADDR shows the previous server's session until the entry goes stale.
	Addr    string
	Recheck time.Duration
	Warn    time.Duration
	Now     func() time.Time

	mu sync.Mutex
	// lastAttempt is the clock time of the last server call Status made, zero
	// until the first one. lastStamp is the token stamp as of that call, used
	// to detect a re-login mid-window. lastStatus is what a throttled call
	// returns instead of contacting the server. forceNext makes the next call
	// bypass the throttle once; see Force. All four are guarded by mu.
	lastAttempt time.Time
	lastStamp   TokenStamp
	lastStatus  Status
	forceNext   bool
}

// Force makes the next Status call contact the server even if the Recheck
// window has not elapsed and the token stamp is unchanged. It exists for
// user-initiated refresh (the "Refresh now" menu item, and a login/logout
// attempt completing) and is safe to call from any goroutine.
func (p *Poller) Force() {
	p.mu.Lock()
	p.forceNext = true
	p.mu.Unlock()
}

func (p *Poller) Status(ctx context.Context) Status {
	now := p.Now()

	token, err := ReadToken(p.TokenPath)
	if err != nil {
		// Deliberately collapsed: every ReadToken error (missing file, unreadable,
		// blank contents) means there is no usable token, so the honest readout is
		// signed-out with the cache cleared. This is not the network path — there
		// is no "degraded" here because there is nothing to be degraded about; the
		// user simply cannot authenticate with what's on disk.
		_ = DeleteCache(p.CachePath)
		return p.recordStatus(Status{State: StateSignedOut})
	}

	stamp := StampToken(p.TokenPath)

	// Snapshot the throttle state under the lock, then decide with the
	// snapshot rather than holding the lock across LoadCache/the decision:
	// none of that touches the guarded fields, and the network call further
	// below never runs with mu held.
	p.mu.Lock()
	forceNext := p.forceNext
	lastAttempt := p.lastAttempt
	lastStamp := p.lastStamp
	lastStatus := p.lastStatus
	p.mu.Unlock()

	// A forced refresh (the "Refresh now" menu item) must reach the server on
	// the very next call even if the cache looks fresh — that is the entire
	// point of asking for one. Skip the fresh-cache shortcut in that case only.
	if !forceNext {
		if c, ok := LoadCache(p.CachePath); ok && c.Fresh(now, p.Recheck, stamp, p.Addr) {
			return p.recordStatus(p.statusFrom(c, now, true))
		}
	}

	// The audit-log throttle: a usable fresh cache already returned above, so
	// everything from here on is a path that would otherwise hit the server
	// every single call — an expired cache, a cache that failed to write, no
	// cache at all, or a transport error with nothing to fall back on. Without
	// this check, any of those states means one authenticated request per poll
	// tick forever. The stamp comparison is what still notices a fresh
	// terminal login within a second rather than waiting out the window.
	callServer := forceNext || stamp != lastStamp || lastAttempt.IsZero() || now.Sub(lastAttempt) >= p.Recheck
	if !callServer {
		return lastStatus
	}

	p.mu.Lock()
	p.lastAttempt = now
	p.lastStamp = stamp
	p.forceNext = false
	p.mu.Unlock()

	info, err := p.Client.LookupSelf(ctx, token, now)
	switch {
	case errors.Is(err, ErrForbidden):
		// The server actively rejected this token: genuinely signed out.
		_ = DeleteCache(p.CachePath)
		return p.recordStatus(Status{State: StateSignedOut})

	case err != nil:
		// Transport failure. The token is probably still fine — keep counting
		// down from cache and show degraded rather than crying logout. The
		// entry must still be OUR server's: falling back to another server's
		// session would misreport the identity, just with a dimmed icon.
		if c, ok := LoadCache(p.CachePath); ok && c.Addr == p.Addr {
			return p.recordStatus(p.statusFrom(c, now, false))
		}
		return p.recordStatus(Status{State: StateDegraded})
	}

	c := Cache{
		CheckedAt:    now.Unix(),
		ExpiresAt:    info.ExpiresAt.Unix(),
		NeverExpires: info.NeverExpires,
		Name:         info.Name,
		Policies:     info.Policies,
		Addr:         p.Addr,
		Token:        stamp,
	}
	_ = SaveCache(p.CachePath, c)
	return p.recordStatus(p.statusFrom(c, now, true))
}

// recordStatus stores s as lastStatus (what a throttled call will return
// next) and returns it, taking mu only for the assignment itself.
func (p *Poller) recordStatus(s Status) Status {
	p.mu.Lock()
	p.lastStatus = s
	p.mu.Unlock()
	return s
}

func (p *Poller) statusFrom(c Cache, now time.Time, reachable bool) Status {
	s := Status{Name: c.Name, Policies: c.Policies, NeverExpires: c.NeverExpires}
	if c.NeverExpires {
		s.State = StateSignedIn
		if !reachable {
			s.State = StateDegraded
		}
		return s
	}

	expiresAt := time.Unix(c.ExpiresAt, 0)
	remaining := expiresAt.Sub(now)
	s.State = Classify(remaining, reachable, true, p.Warn)
	if remaining < 0 {
		remaining = 0
	}
	s.Remaining = remaining
	s.ExpiresAt = expiresAt
	return s
}
