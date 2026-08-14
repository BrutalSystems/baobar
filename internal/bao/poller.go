package bao

import (
	"context"
	"errors"
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
// Status is called from a single goroutine — the tray's poll goroutine (see
// internal/tray.onReadyNormal) — so lastAttempt, lastStamp, lastStatus, and
// forceNext need no mutex. Force must therefore only ever be called from that
// same goroutine's signalling path (e.g. by writing to the poll goroutine's
// refresh channel), never concurrently with a Status call.
type Poller struct {
	Client    Lookuper
	TokenPath string
	CachePath string
	Recheck   time.Duration
	Warn      time.Duration
	Now       func() time.Time

	// lastAttempt is the clock time of the last server call Status made, zero
	// until the first one. lastStamp is the token stamp as of that call, used
	// to detect a re-login mid-window. lastStatus is what a throttled call
	// returns instead of contacting the server. forceNext makes the next call
	// bypass the throttle once; see Force.
	lastAttempt time.Time
	lastStamp   TokenStamp
	lastStatus  Status
	forceNext   bool
}

// Force makes the next Status call contact the server even if the Recheck
// window has not elapsed and the token stamp is unchanged. It exists for
// user-initiated refresh (the "Refresh now" menu item) and must only be
// called from the same goroutine that calls Status.
func (p *Poller) Force() {
	p.forceNext = true
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
		p.lastStatus = Status{State: StateSignedOut}
		return p.lastStatus
	}

	stamp := StampToken(p.TokenPath)
	// A forced refresh (the "Refresh now" menu item) must reach the server on
	// the very next call even if the cache looks fresh — that is the entire
	// point of asking for one. Skip the fresh-cache shortcut in that case only.
	if !p.forceNext {
		if c, ok := LoadCache(p.CachePath); ok && c.Fresh(now, p.Recheck, stamp) {
			p.lastStatus = p.statusFrom(c, now, true)
			return p.lastStatus
		}
	}

	// The audit-log throttle: a usable fresh cache already returned above, so
	// everything from here on is a path that would otherwise hit the server
	// every single call — an expired cache, a cache that failed to write, no
	// cache at all, or a transport error with nothing to fall back on. Without
	// this check, any of those states means one authenticated request per poll
	// tick forever. The stamp comparison is what still notices a fresh
	// terminal login within a second rather than waiting out the window.
	callServer := p.forceNext || stamp != p.lastStamp || p.lastAttempt.IsZero() || now.Sub(p.lastAttempt) >= p.Recheck
	if !callServer {
		return p.lastStatus
	}
	p.lastAttempt = now
	p.lastStamp = stamp
	p.forceNext = false

	info, err := p.Client.LookupSelf(ctx, token, now)
	switch {
	case errors.Is(err, ErrForbidden):
		// The server actively rejected this token: genuinely signed out.
		_ = DeleteCache(p.CachePath)
		p.lastStatus = Status{State: StateSignedOut}
		return p.lastStatus

	case err != nil:
		// Transport failure. The token is probably still fine — keep counting
		// down from cache and show degraded rather than crying logout.
		if c, ok := LoadCache(p.CachePath); ok {
			p.lastStatus = p.statusFrom(c, now, false)
			return p.lastStatus
		}
		p.lastStatus = Status{State: StateDegraded}
		return p.lastStatus
	}

	c := Cache{
		CheckedAt:    now.Unix(),
		ExpiresAt:    info.ExpiresAt.Unix(),
		NeverExpires: info.NeverExpires,
		Name:         info.Name,
		Policies:     info.Policies,
		Token:        stamp,
	}
	_ = SaveCache(p.CachePath, c)
	p.lastStatus = p.statusFrom(c, now, true)
	return p.lastStatus
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
