package bao

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeLookuper struct {
	info  Info
	err   error
	calls int
}

func (f *fakeLookuper) LookupSelf(_ context.Context, _ string, _ time.Time) (Info, error) {
	f.calls++
	return f.info, f.err
}

// newPoller wires a poller onto a temp dir with a frozen clock.
func newPoller(t *testing.T, l Lookuper, now time.Time) (*Poller, string, string) {
	t.Helper()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, ".vault-token")
	cachePath := filepath.Join(dir, "status.json")
	return &Poller{
		Client:    l,
		TokenPath: tokenPath,
		CachePath: cachePath,
		Recheck:   300 * time.Second,
		Warn:      30 * time.Minute,
		Now:       func() time.Time { return now },
	}, tokenPath, cachePath
}

// writeToken writes a token file and returns the stamp a cache entry must carry
// to be considered fresh for it.
func writeToken(t *testing.T, path, token string) TokenStamp {
	t.Helper()
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	return StampToken(path)
}

func TestStatusWithoutTokenFileNeverCallsTheServer(t *testing.T) {
	l := &fakeLookuper{}
	p, _, _ := newPoller(t, l, time.Unix(10_000, 0))

	got := p.Status(context.Background())
	if got.State != StateSignedOut {
		t.Errorf("State = %v, want signed-out", got.State)
	}
	if l.calls != 0 {
		t.Errorf("server called %d times with no token on disk", l.calls)
	}
}

// The audit-log invariant, enforced: a fresh cache means no request.
func TestStatusUsesFreshCacheWithoutCallingTheServer(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{}
	p, tokenPath, cachePath := newPoller(t, l, now)
	stamp := writeToken(t, tokenPath, "hvs.abc")
	SaveCache(cachePath, Cache{
		CheckedAt: 9_900, ExpiresAt: 10_000 + int64(6*time.Hour/time.Second),
		Name: "userpass-dev", Policies: []string{"admin"}, Token: stamp,
	})

	got := p.Status(context.Background())
	if l.calls != 0 {
		t.Errorf("server called %d times despite a fresh cache", l.calls)
	}
	if got.State != StateSignedIn {
		t.Errorf("State = %v, want signed-in", got.State)
	}
	if got.Remaining != 6*time.Hour {
		t.Errorf("Remaining = %v, want 6h", got.Remaining)
	}
	if got.ExpiresAt.Unix() != 10_000+int64(6*time.Hour/time.Second) {
		t.Errorf("ExpiresAt = %v, want the cached absolute expiry", got.ExpiresAt)
	}
	if got.Name != "userpass-dev" {
		t.Errorf("Name = %q, want the cached name", got.Name)
	}
}

// Logging in again from a terminal replaces the token file. The cache is still
// inside its recheck window, but it describes a token that no longer exists, so
// it must be re-checked rather than served.
func TestStatusRechecksWhenTokenFileChanges(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{info: Info{ExpiresAt: now.Add(8 * time.Hour), Name: "userpass-dev"}}
	p, tokenPath, cachePath := newPoller(t, l, now)

	writeToken(t, tokenPath, "hvs.old")
	SaveCache(cachePath, Cache{
		CheckedAt: 9_900, ExpiresAt: 10_000 + int64(90*time.Minute/time.Second),
		Name: "userpass-dev", Token: StampToken(tokenPath),
	})

	// The user re-authenticates in a terminal: same path, different content.
	writeToken(t, tokenPath, "hvs.freshly-issued-and-longer")

	got := p.Status(context.Background())
	if l.calls != 1 {
		t.Fatalf("server called %d times, want 1 — a new token must force a lookup", l.calls)
	}
	if got.Remaining != 8*time.Hour {
		t.Errorf("Remaining = %v, want the new token's 8h, not the cached 90m", got.Remaining)
	}
}

func TestStatusRefreshesStaleCache(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{info: Info{
		ExpiresAt: now.Add(2 * time.Hour), Name: "userpass-dev", Policies: []string{"admin"},
	}}
	p, tokenPath, cachePath := newPoller(t, l, now)
	stamp := writeToken(t, tokenPath, "hvs.abc")
	SaveCache(cachePath, Cache{CheckedAt: 1, ExpiresAt: 99_999, Name: "old", Token: stamp})

	got := p.Status(context.Background())
	if l.calls != 1 {
		t.Errorf("server called %d times, want 1", l.calls)
	}
	if got.Remaining != 2*time.Hour {
		t.Errorf("Remaining = %v, want 2h", got.Remaining)
	}

	c, ok := LoadCache(cachePath)
	if !ok || c.CheckedAt != now.Unix() || c.Name != "userpass-dev" {
		t.Errorf("cache not refreshed: %+v", c)
	}
}

func TestStatusExpiringWithinWarningWindow(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{info: Info{ExpiresAt: now.Add(22 * time.Minute), Name: "userpass-dev"}}
	p, tokenPath, _ := newPoller(t, l, now)
	writeToken(t, tokenPath, "hvs.abc")

	if got := p.Status(context.Background()); got.State != StateExpiring {
		t.Errorf("State = %v, want expiring", got.State)
	}
}

// A rejected token is genuinely signed out, and the stale cache must go.
func TestStatusForbiddenClearsCache(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{err: ErrForbidden}
	p, tokenPath, cachePath := newPoller(t, l, now)
	stamp := writeToken(t, tokenPath, "hvs.revoked")
	SaveCache(cachePath, Cache{CheckedAt: 1, ExpiresAt: 99_999, Name: "old", Token: stamp})

	got := p.Status(context.Background())
	if got.State != StateSignedOut {
		t.Errorf("State = %v, want signed-out", got.State)
	}
	if _, ok := LoadCache(cachePath); ok {
		t.Error("cache survived a 403")
	}
}

// THE regression to guard: an unreachable server is not a logout.
func TestStatusNetworkErrorIsDegradedNotSignedOut(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{err: errors.New("dial tcp: connection refused")}
	p, tokenPath, cachePath := newPoller(t, l, now)
	stamp := writeToken(t, tokenPath, "hvs.abc")
	SaveCache(cachePath, Cache{
		CheckedAt: 1, ExpiresAt: 10_000 + int64(6*time.Hour/time.Second),
		Name: "userpass-dev", Token: stamp,
	})

	got := p.Status(context.Background())
	if got.State != StateDegraded {
		t.Fatalf("State = %v, want degraded", got.State)
	}
	if got.Remaining != 6*time.Hour {
		t.Errorf("Remaining = %v, want the cached 6h countdown to keep running", got.Remaining)
	}
	if _, ok := LoadCache(cachePath); !ok {
		t.Error("cache was cleared by a mere network failure")
	}
}

func TestStatusNetworkErrorWithNoCacheIsDegraded(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{err: errors.New("dial tcp: connection refused")}
	p, tokenPath, _ := newPoller(t, l, now)
	writeToken(t, tokenPath, "hvs.abc")

	got := p.Status(context.Background())
	if got.State != StateDegraded {
		t.Errorf("State = %v, want degraded", got.State)
	}
	if got.Remaining != 0 {
		t.Errorf("Remaining = %v, want 0 (unknown)", got.Remaining)
	}
}

// A cached token whose expiry has passed is out, even offline.
func TestStatusExpiredCacheOfflineIsSignedOut(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{err: errors.New("dial tcp: connection refused")}
	p, tokenPath, cachePath := newPoller(t, l, now)
	stamp := writeToken(t, tokenPath, "hvs.abc")
	SaveCache(cachePath, Cache{CheckedAt: 1, ExpiresAt: 9_000, Name: "userpass-dev", Token: stamp})

	if got := p.Status(context.Background()); got.State != StateSignedOut {
		t.Errorf("State = %v, want signed-out", got.State)
	}
}

func TestStatusNonExpiringToken(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{info: Info{NeverExpires: true, Name: "root", Policies: []string{"root"}}}
	p, tokenPath, _ := newPoller(t, l, now)
	writeToken(t, tokenPath, "hvs.root")

	got := p.Status(context.Background())
	if got.State != StateSignedIn || !got.NeverExpires {
		t.Errorf("got %+v, want signed-in and NeverExpires", got)
	}
}
