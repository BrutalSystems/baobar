package bao

import (
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	const warn = 30 * time.Minute

	tests := []struct {
		name        string
		remaining   time.Duration
		reachable   bool
		expiryKnown bool
		want        State
	}{
		{"plenty of time", 6 * time.Hour, true, true, StateSignedIn},
		{"inside the warning window", 22 * time.Minute, true, true, StateExpiring},
		{"exactly at the threshold", warn, true, true, StateExpiring},
		{"just past the threshold", warn + time.Second, true, true, StateSignedIn},
		{"expired", -time.Second, true, true, StateSignedOut},
		{"expiring exactly now", 0, true, true, StateSignedOut},

		// A network failure must never read as logged out: the token is still
		// valid and nagging the user into a pointless re-login is the bug.
		{"unreachable with time left", 6 * time.Hour, false, true, StateDegraded},
		{"unreachable inside warning window", 22 * time.Minute, false, true, StateDegraded},

		// Expiry is absolute, so a known-expired token is out regardless of
		// whether we can reach the server to confirm it.
		{"unreachable and expired", -time.Second, false, true, StateSignedOut},

		// Token file present, never successfully looked up, server down.
		{"unreachable with no known expiry", 0, false, false, StateDegraded},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.remaining, tc.reachable, tc.expiryKnown, warn)
			if got != tc.want {
				t.Errorf("Classify(%v, reachable=%v, known=%v) = %v, want %v",
					tc.remaining, tc.reachable, tc.expiryKnown, got, tc.want)
			}
		})
	}
}

func TestStateString(t *testing.T) {
	for state, want := range map[State]string{
		StateSignedOut: "signed-out",
		StateSignedIn:  "signed-in",
		StateExpiring:  "expiring",
		StateDegraded:  "degraded",
	} {
		if got := state.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", state, got, want)
		}
	}
}
