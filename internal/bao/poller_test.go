package bao

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// testAddr is the server every poller in this file talks to.
const testAddr = "https://bao.example.com"

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
		Addr:      testAddr,
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
	SaveCache(cachePath, Cache{Addr: testAddr,
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
	SaveCache(cachePath, Cache{Addr: testAddr,
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
	SaveCache(cachePath, Cache{Addr: testAddr, CheckedAt: 1, ExpiresAt: 99_999, Name: "old", Token: stamp})

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
	SaveCache(cachePath, Cache{Addr: testAddr, CheckedAt: 1, ExpiresAt: 99_999, Name: "old", Token: stamp})

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
	SaveCache(cachePath, Cache{Addr: testAddr,
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
	SaveCache(cachePath, Cache{Addr: testAddr, CheckedAt: 1, ExpiresAt: 9_000, Name: "userpass-dev", Token: stamp})

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

// A root token is still at the mercy of the network for freshness: if the
// server can't be reached, "never expires" must not be read as "definitely
// still valid." Without this, an outage would render a root token as fully
// signed-in when its cache entry could be arbitrarily old.
func TestStatusNonExpiringTokenDegradedOnTransportError(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{err: errors.New("dial tcp: connection refused")}
	p, tokenPath, cachePath := newPoller(t, l, now)
	stamp := writeToken(t, tokenPath, "hvs.root")
	SaveCache(cachePath, Cache{Addr: testAddr,
		CheckedAt: 1, NeverExpires: true, Name: "root", Policies: []string{"root"}, Token: stamp,
	})

	got := p.Status(context.Background())
	if got.State != StateDegraded {
		t.Errorf("State = %v, want degraded", got.State)
	}
	if !got.NeverExpires {
		t.Error("NeverExpires = false, want true")
	}
}

// The audit-log invariant end to end: the cache the poller itself writes on a
// cold call must be the cache the very next call finds fresh, with no second
// server round trip. Every other freshness test hand-builds the Cache outside
// the poller; this one proves the poller's own SaveCache produces a usable
// stamp.
func TestStatusSecondCallUsesTheCacheItJustWrote(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{info: Info{
		ExpiresAt: now.Add(6 * time.Hour), Name: "userpass-dev", Policies: []string{"admin"},
	}}
	p, tokenPath, _ := newPoller(t, l, now)
	writeToken(t, tokenPath, "hvs.abc")

	first := p.Status(context.Background())
	if l.calls != 1 {
		t.Fatalf("server called %d times on first Status, want 1", l.calls)
	}

	second := p.Status(context.Background())
	if l.calls != 1 {
		t.Fatalf("server called %d times after a second Status with the same clock, want still 1", l.calls)
	}
	if second.State != first.State || second.Remaining != first.Remaining ||
		second.Name != first.Name || !second.ExpiresAt.Equal(first.ExpiresAt) {
		t.Errorf("second Status = %+v, want it to match first = %+v", second, first)
	}
}

// --- The audit-log throttle (fix wave item 1) -----------------------------
//
// These prove the throttle is TIME-based and unconditional: it holds even
// when there is never a usable cache to serve from, which is exactly the
// case the old ("cache exists and is fresh") throttle failed to cover. Each
// test drives a simulated clock one second at a time for 600 ticks — ten
// simulated minutes against the default 300s Recheck — and asserts the
// server was called at most twice: once to discover the failure, once more
// when the Recheck window elapses at the 300s mark. Without the fix, each of
// these scenarios calls the server on every one of the 600 ticks.

// tickingPoller returns a Poller wired to a clock the test can advance
// independently of wall time, plus a function to advance it by one second.
func tickingPoller(t *testing.T, l Lookuper, cachePath string) (*Poller, string, func()) {
	t.Helper()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, ".vault-token")
	if cachePath == "" {
		cachePath = filepath.Join(dir, "status.json")
	}
	now := time.Unix(10_000, 0)
	p := &Poller{
		Client:    l,
		TokenPath: tokenPath,
		CachePath: cachePath,
		Recheck:   300 * time.Second,
		Warn:      30 * time.Minute,
		Now:       func() time.Time { return now },
	}
	tick := func() { now = now.Add(time.Second) }
	return p, tokenPath, tick
}

// runTicks calls Status once per simulated second for 600 seconds (t=0..599),
// so the 300s Recheck boundary is crossed exactly once.
func runTicks(p *Poller, tick func()) {
	for i := 0; i < 600; i++ {
		p.Status(context.Background())
		tick()
	}
}

func TestThrottleHoldsForExpiredTokenReturningForbidden(t *testing.T) {
	l := &fakeLookuper{err: ErrForbidden}
	p, tokenPath, tick := tickingPoller(t, l, "")
	writeToken(t, tokenPath, "hvs.expired")

	runTicks(p, tick)

	if l.calls > 2 {
		t.Errorf("server called %d times over 600 ticks, want at most 2", l.calls)
	}
}

func TestThrottleHoldsWhenSaveCacheFails(t *testing.T) {
	dir := t.TempDir()
	noWrite := filepath.Join(dir, "nowrite")
	if err := os.Mkdir(noWrite, 0o500); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { os.Chmod(noWrite, 0o700) }) // let TempDir clean up

	l := &fakeLookuper{info: Info{ExpiresAt: time.Unix(10_000, 0).Add(6 * time.Hour), Name: "userpass-dev"}}
	p, tokenPath, tick := tickingPoller(t, l, filepath.Join(noWrite, "status.json"))
	writeToken(t, tokenPath, "hvs.abc")

	// Confirm the premise: the cache path really is unwritable.
	if err := SaveCache(p.CachePath, Cache{}); err == nil {
		t.Fatal("test setup: SaveCache unexpectedly succeeded against a read-only directory")
	}

	runTicks(p, tick)

	if l.calls > 2 {
		t.Errorf("server called %d times over 600 ticks with a failing SaveCache, want at most 2", l.calls)
	}
}

func TestThrottleHoldsForTransportErrorWithNoCache(t *testing.T) {
	l := &fakeLookuper{err: errors.New("dial tcp: connection refused")}
	p, tokenPath, tick := tickingPoller(t, l, "")
	writeToken(t, tokenPath, "hvs.abc")

	runTicks(p, tick)

	if l.calls > 2 {
		t.Errorf("server called %d times over 600 ticks, want at most 2", l.calls)
	}
}

// Regression guard: the healthy path must stay just as quiet as the failing
// ones now do.
func TestThrottleHoldsForHealthySignedIn(t *testing.T) {
	l := &fakeLookuper{info: Info{ExpiresAt: time.Unix(10_000, 0).Add(6 * time.Hour), Name: "userpass-dev"}}
	p, tokenPath, tick := tickingPoller(t, l, "")
	writeToken(t, tokenPath, "hvs.abc")

	runTicks(p, tick)

	if l.calls > 2 {
		t.Errorf("server called %d times over 600 ticks while healthy, want at most 2", l.calls)
	}
}

// A token file that changes mid-window (a terminal re-login) must force an
// immediate call even though the Recheck window has not elapsed.
func TestThrottleForcesImmediateCallWhenStampChanges(t *testing.T) {
	l := &fakeLookuper{info: Info{ExpiresAt: time.Unix(10_000, 0).Add(6 * time.Hour), Name: "userpass-dev"}}
	p, tokenPath, tick := tickingPoller(t, l, "")
	writeToken(t, tokenPath, "hvs.old")

	p.Status(context.Background())
	if l.calls != 1 {
		t.Fatalf("server called %d times on the first call, want 1", l.calls)
	}

	// Advance ten seconds — well inside the 300s window — with no change.
	for i := 0; i < 10; i++ {
		tick()
		p.Status(context.Background())
	}
	if l.calls != 1 {
		t.Fatalf("server called %d times inside the window with no change, want still 1", l.calls)
	}

	// A different-length token guarantees a different stamp regardless of
	// mtime resolution.
	writeToken(t, tokenPath, "hvs.freshly-issued-and-much-longer-than-before")
	tick()
	got := p.Status(context.Background())
	if l.calls != 2 {
		t.Fatalf("server called %d times after the token file changed mid-window, want 2 (immediate)", l.calls)
	}
	if got.State == StateDegraded {
		t.Errorf("State = %v after a successful re-login lookup, want a live state", got.State)
	}

	// And it re-throttles from there: a few more ticks with no further change
	// must not call again.
	for i := 0; i < 10; i++ {
		tick()
		p.Status(context.Background())
	}
	if l.calls != 2 {
		t.Errorf("server called %d times after re-throttling, want still 2", l.calls)
	}
}

// Force must cause exactly one immediate extra call, then re-throttle.
func TestForceCausesOneImmediateCallThenRethrottles(t *testing.T) {
	l := &fakeLookuper{info: Info{ExpiresAt: time.Unix(10_000, 0).Add(6 * time.Hour), Name: "userpass-dev"}}
	p, tokenPath, tick := tickingPoller(t, l, "")
	writeToken(t, tokenPath, "hvs.abc")

	p.Status(context.Background())
	if l.calls != 1 {
		t.Fatalf("server called %d times on the first call, want 1", l.calls)
	}

	for i := 0; i < 5; i++ {
		tick()
		p.Status(context.Background())
	}
	if l.calls != 1 {
		t.Fatalf("server called %d times before Force, want still 1", l.calls)
	}

	p.Force()
	tick()
	p.Status(context.Background())
	if l.calls != 2 {
		t.Fatalf("server called %d times right after Force, want 2", l.calls)
	}

	// A second immediate call must NOT call again: Force is one-shot.
	tick()
	p.Status(context.Background())
	if l.calls != 2 {
		t.Errorf("server called %d times on the call after Force, want still 2 (Force is one-shot)", l.calls)
	}

	for i := 0; i < 10; i++ {
		tick()
		p.Status(context.Background())
	}
	if l.calls != 2 {
		t.Errorf("server called %d times while re-throttled after Force, want still 2", l.calls)
	}
}

// Force is documented as safe to call from any goroutine — a menu click, or
// a logout/login attempt completing on its own goroutine — concurrently with
// the poll goroutine's Status calls. This hammers exactly that: one goroutine
// calling Status in a tight loop (standing in for the poll goroutine) while
// several others call Force concurrently (standing in for menu clicks). It
// asserts nothing about counts — the point is purely that `go test -race`
// reports no data race on Poller's throttle fields. Verified to actually
// catch the bug it guards: run under -race against a Poller with the mutex
// removed (reverting to the single-goroutine-only version), this test failed
// with "WARNING: DATA RACE" pointing at Status's read and Force's write of
// forceNext, before the mutex was restored. See the fix-wave report for the
// captured output.
func TestForceIsRaceSafeAgainstConcurrentStatus(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{info: Info{ExpiresAt: now.Add(6 * time.Hour), Name: "userpass-dev"}}
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, ".vault-token")
	p := &Poller{
		Client:    l,
		TokenPath: tokenPath,
		CachePath: filepath.Join(dir, "status.json"),
		Recheck:   300 * time.Second,
		Warn:      30 * time.Minute,
		Now:       func() time.Time { return now },
	}
	writeToken(t, tokenPath, "hvs.abc")

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// The poll goroutine: calls Status back to back until told to stop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				p.Status(context.Background())
			}
		}
	}()

	// Several goroutines standing in for menu clicks / a completed
	// logout-login goroutine, all calling Force concurrently with the
	// Status loop above.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					p.Force()
				}
			}
		}()
	}

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// ErrForbidden must be recognized even when a future client change wraps it,
// so the check must stay errors.Is, not ==.
func TestStatusForbiddenClearsCacheWhenWrapped(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{err: fmt.Errorf("lookup failed: %w", ErrForbidden)}
	p, tokenPath, cachePath := newPoller(t, l, now)
	stamp := writeToken(t, tokenPath, "hvs.revoked")
	SaveCache(cachePath, Cache{Addr: testAddr, CheckedAt: 1, ExpiresAt: 99_999, Name: "old", Token: stamp})

	got := p.Status(context.Background())
	if got.State != StateSignedOut {
		t.Errorf("State = %v, want signed-out", got.State)
	}
	if _, ok := LoadCache(cachePath); ok {
		t.Error("cache survived a wrapped 403")
	}
}

// Reproduces a bug seen live: with a cache written by one server still fresh,
// pointing Baobar at a DIFFERENT address served the first server's identity,
// policies, and countdown — and made no request at all while doing so. The app
// reported the user signed in to a server it had never contacted.
func TestStatusIgnoresCacheFromAnotherServer(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{err: ErrForbidden}
	p, tokenPath, cachePath := newPoller(t, l, now)
	stamp := writeToken(t, tokenPath, "hvs.abc")

	// A perfectly fresh entry — but from a different server.
	SaveCache(cachePath, Cache{
		Addr: "https://other.example.com", CheckedAt: 9_900,
		ExpiresAt: 10_000 + int64(6*time.Hour/time.Second),
		Name:      "userpass-dev", Policies: []string{"admin"}, Token: stamp,
	})

	got := p.Status(context.Background())

	if l.calls != 1 {
		t.Errorf("server called %d times, want 1 — a cache from another server must not be served", l.calls)
	}
	if got.Name == "userpass-dev" {
		t.Error("served the other server's identity")
	}
	if got.State != StateSignedOut {
		t.Errorf("State = %v, want signed-out (this server rejected the token)", got.State)
	}
}

// The address recorded must be the poller's own, so the next run can tell.
func TestStatusStampsTheCacheWithItsServer(t *testing.T) {
	now := time.Unix(10_000, 0)
	l := &fakeLookuper{info: Info{ExpiresAt: now.Add(2 * time.Hour), Name: "userpass-dev"}}
	p, tokenPath, cachePath := newPoller(t, l, now)
	writeToken(t, tokenPath, "hvs.abc")

	p.Status(context.Background())

	c, ok := LoadCache(cachePath)
	if !ok || c.Addr != testAddr {
		t.Errorf("cache Addr = %q, want %q", c.Addr, testAddr)
	}
}
