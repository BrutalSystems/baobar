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
// It calls the server at most once per Recheck window. Do not "fix" a stale-
// looking countdown by shortening Recheck — the countdown is recomputed locally
// on every call and is never stale. See the design doc's audit-log invariant.
//
// Status blocks on the network when the cache is cold, so callers must run it
// off any UI goroutine (see internal/tray).
type Poller struct {
	Client    Lookuper
	TokenPath string
	CachePath string
	Recheck   time.Duration
	Warn      time.Duration
	Now       func() time.Time
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
		return Status{State: StateSignedOut}
	}

	stamp := StampToken(p.TokenPath)
	if c, ok := LoadCache(p.CachePath); ok && c.Fresh(now, p.Recheck, stamp) {
		return p.statusFrom(c, now, true)
	}

	info, err := p.Client.LookupSelf(ctx, token, now)
	switch {
	case errors.Is(err, ErrForbidden):
		// The server actively rejected this token: genuinely signed out.
		_ = DeleteCache(p.CachePath)
		return Status{State: StateSignedOut}

	case err != nil:
		// Transport failure. The token is probably still fine — keep counting
		// down from cache and show degraded rather than crying logout.
		if c, ok := LoadCache(p.CachePath); ok {
			return p.statusFrom(c, now, false)
		}
		return Status{State: StateDegraded}
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
	return p.statusFrom(c, now, true)
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
