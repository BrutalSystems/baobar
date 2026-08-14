package notify

import (
	"testing"
	"time"
)

func newTestTracker() *Tracker {
	return NewTracker(30*time.Minute, 5*time.Minute)
}

func TestTrackerFiresOncePerThreshold(t *testing.T) {
	tr := newTestTracker()
	const token = int64(2000)

	if got := tr.Crossed(token, 6*time.Hour); len(got) != 0 {
		t.Errorf("Crossed at 6h = %v, want none", got)
	}
	got := tr.Crossed(token, 29*time.Minute)
	if len(got) != 1 || got[0] != 30*time.Minute {
		t.Fatalf("Crossed at 29m = %v, want [30m]", got)
	}
	// Ticking again inside the same window must stay silent.
	if got := tr.Crossed(token, 28*time.Minute); len(got) != 0 {
		t.Errorf("Crossed at 28m = %v, want none", got)
	}
	got = tr.Crossed(token, 4*time.Minute)
	if len(got) != 1 || got[0] != 5*time.Minute {
		t.Fatalf("Crossed at 4m = %v, want [5m]", got)
	}
	if got := tr.Crossed(token, 3*time.Minute); len(got) != 0 {
		t.Errorf("Crossed at 3m = %v, want none", got)
	}
}

// Jumping straight past both thresholds reports both, in descending order.
func TestTrackerReportsMultipleCrossingsAtOnce(t *testing.T) {
	tr := newTestTracker()
	got := tr.Crossed(2000, 2*time.Minute)
	if len(got) != 2 || got[0] != 30*time.Minute || got[1] != 5*time.Minute {
		t.Fatalf("Crossed = %v, want [30m 5m]", got)
	}
}

// A new token (new expiry) is a new countdown: warnings must arm again.
func TestTrackerResetsOnNewToken(t *testing.T) {
	tr := newTestTracker()
	tr.Crossed(2000, 29*time.Minute)

	got := tr.Crossed(9999, 29*time.Minute)
	if len(got) != 1 || got[0] != 30*time.Minute {
		t.Fatalf("Crossed after re-login = %v, want [30m]", got)
	}
}

// Already expired is the tray's problem, not the notifier's.
func TestTrackerSilentWhenExpired(t *testing.T) {
	tr := newTestTracker()
	if got := tr.Crossed(2000, 0); len(got) != 0 {
		t.Errorf("Crossed at 0 = %v, want none", got)
	}
	if got := tr.Crossed(2000, -time.Minute); len(got) != 0 {
		t.Errorf("Crossed at -1m = %v, want none", got)
	}
}

// Thresholds passed out of order must be sorted internally (longest first).
func TestTrackerSortsThresholds(t *testing.T) {
	tr := NewTracker(5*time.Minute, 30*time.Minute)
	got := tr.Crossed(2000, 2*time.Minute)
	if len(got) != 2 || got[0] != 30*time.Minute || got[1] != 5*time.Minute {
		t.Fatalf("Crossed = %v, want [30m 5m] (descending order)", got)
	}
}

// Re-arm must fully reset the fired map, not just clear one threshold.
func TestTrackerFullResetOnNewToken(t *testing.T) {
	tr := newTestTracker()

	// Cross both thresholds on token 2000
	got := tr.Crossed(2000, 2*time.Minute)
	if len(got) != 2 {
		t.Fatalf("initial cross = %v, want [30m 5m]", got)
	}

	// Switch to token 9999 and cross both again; both must fire
	got = tr.Crossed(9999, 2*time.Minute)
	if len(got) != 2 || got[0] != 30*time.Minute || got[1] != 5*time.Minute {
		t.Fatalf("after re-login = %v, want [30m 5m]", got)
	}
}
