// Package bao holds Baobar's token state machine, API client, and cache.
// It knows nothing about menus and never shells out to the bao CLI.
package bao

import "time"

type State int

const (
	StateSignedOut State = iota
	StateSignedIn
	StateExpiring
	StateDegraded
)

func (s State) String() string {
	switch s {
	case StateSignedIn:
		return "signed-in"
	case StateExpiring:
		return "expiring"
	case StateDegraded:
		return "degraded"
	default:
		return "signed-out"
	}
}

// Status is the full picture the tray renders.
type Status struct {
	State     State
	Remaining time.Duration // clamped at zero; meaningless when NeverExpires
	// ExpiresAt is the absolute expiry, zero when unknown. The tray uses it to
	// identify the current token when tracking notification thresholds — do not
	// remove it and reconstruct it as now+Remaining at the call site.
	ExpiresAt    time.Time
	Name         string   // display_name, e.g. "userpass-dev"
	Policies     []string // "default" already filtered out
	NeverExpires bool     // root and other non-expiring tokens
}

// Classify decides the state. reachable is false when the server could not be
// reached and the caller is falling back to cached data; expiryKnown is false
// when no cached expiry exists at all.
func Classify(remaining time.Duration, reachable, expiryKnown bool, warn time.Duration) State {
	if !reachable {
		if expiryKnown && remaining <= 0 {
			return StateSignedOut
		}
		return StateDegraded
	}
	if remaining <= 0 {
		return StateSignedOut
	}
	if remaining <= warn {
		return StateExpiring
	}
	return StateSignedIn
}
