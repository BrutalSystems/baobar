// Package notify handles expiry warnings: deciding when one is due, and showing it.
package notify

import (
	"sort"
	"time"
)

// Tracker fires each threshold at most once per token. tokenKey identifies the
// current token (its expiry, in Unix seconds) so a re-login re-arms every warning.
type Tracker struct {
	thresholds []time.Duration
	key        int64
	fired      map[time.Duration]bool
}

func NewTracker(thresholds ...time.Duration) *Tracker {
	sorted := append([]time.Duration(nil), thresholds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] > sorted[j] })
	return &Tracker{thresholds: sorted, fired: map[time.Duration]bool{}}
}

// Crossed returns the thresholds newly crossed by this tick, longest first.
func (t *Tracker) Crossed(tokenKey int64, remaining time.Duration) []time.Duration {
	if tokenKey != t.key {
		t.key = tokenKey
		t.fired = map[time.Duration]bool{}
	}
	if remaining <= 0 {
		return nil
	}

	var due []time.Duration
	for _, th := range t.thresholds {
		if remaining <= th && !t.fired[th] {
			t.fired[th] = true
			due = append(due, th)
		}
	}
	return due
}
