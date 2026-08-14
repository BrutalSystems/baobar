package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/brutalsystems/baobar/internal/bao"
)

func TestHuman(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{6*time.Hour + 19*time.Minute, "6h19m"},
		{22 * time.Minute, "22m"},
		{90 * time.Minute, "1h30m"},
		{time.Hour + 5*time.Minute, "1h05m"}, // zero-padded past the hour
		{0, "0m"},
		{-time.Minute, "0m"},
		{59 * time.Second, "0m"},
	}
	for _, tc := range tests {
		if got := Human(tc.in); got != tc.want {
			t.Errorf("Human(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLabel(t *testing.T) {
	tests := []struct {
		name string
		in   bao.Status
		want string
	}{
		{"signed in", bao.Status{State: bao.StateSignedIn, Remaining: 6*time.Hour + 19*time.Minute}, "🔓 6h19m"},
		{"expiring", bao.Status{State: bao.StateExpiring, Remaining: 22 * time.Minute}, "🟠 22m"},
		{"signed out", bao.Status{State: bao.StateSignedOut}, "🔒 login"},
		{"degraded with a cached countdown", bao.Status{State: bao.StateDegraded, Remaining: 6 * time.Hour}, "🌐 6h00m"},
		{"degraded with nothing cached", bao.Status{State: bao.StateDegraded}, "🌐 ?"},
		{"non-expiring", bao.Status{State: bao.StateSignedIn, NeverExpires: true}, "🔓 ∞"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Label(tc.in); got != tc.want {
				t.Errorf("Label() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The tooltip carries the whole story on Windows, where Label is never shown.
func TestTooltipCarriesFullStateForWindows(t *testing.T) {
	tests := []struct {
		name   string
		status bao.Status
		wants  []string
	}{
		{
			"signed in",
			bao.Status{State: bao.StateSignedIn, Remaining: 6 * time.Hour, Name: "userpass-dev", Policies: []string{"admin", "deploy"}},
			[]string{"OpenBao", "userpass-dev", "6h00m"},
		},
		{
			"expiring",
			bao.Status{State: bao.StateExpiring, Remaining: 22 * time.Minute, Name: "userpass-dev", Policies: []string{"admin"}},
			[]string{"OpenBao", "userpass-dev", "22m"},
		},
		{
			"degraded with a cached countdown",
			bao.Status{State: bao.StateDegraded, Remaining: time.Hour, Name: "cached-user"},
			[]string{"unreachable", "1h00m"},
		},
		{
			"degraded with nothing cached",
			bao.Status{State: bao.StateDegraded, Name: "unknown"},
			[]string{"unreachable"},
		},
		{
			"signed out",
			bao.Status{State: bao.StateSignedOut},
			[]string{"Not signed in"},
		},
		{
			"signed in and NeverExpires",
			bao.Status{State: bao.StateSignedIn, NeverExpires: true, Name: "root"},
			[]string{"OpenBao", "root", "does not expire"},
		},
		{
			"degraded and NeverExpires",
			bao.Status{State: bao.StateDegraded, NeverExpires: true, Name: "cached-root"},
			[]string{"unreachable", "does not expire"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Tooltip(tc.status)
			for _, want := range tc.wants {
				if !strings.Contains(got, want) {
					t.Errorf("Tooltip() = %q, missing %q", got, want)
				}
			}
		})
	}
}

func TestMenuLines(t *testing.T) {
	who, policies, expires := MenuLines(bao.Status{
		State: bao.StateSignedIn, Remaining: 6 * time.Hour,
		Name: "userpass-dev", Policies: []string{"admin", "deploy"},
	})
	if who != "Signed in as userpass-dev" {
		t.Errorf("who = %q", who)
	}
	if policies != "Policies: admin, deploy" {
		t.Errorf("policies = %q", policies)
	}
	if expires != "Expires in 6h00m" {
		t.Errorf("expires = %q", expires)
	}

	// An empty string means "hide this row".
	who, policies, expires = MenuLines(bao.Status{State: bao.StateSignedOut})
	if who != "Not signed in" || policies != "" || expires != "" {
		t.Errorf("signed out = %q / %q / %q, want the last two empty", who, policies, expires)
	}

	who, policies, expires = MenuLines(bao.Status{State: bao.StateDegraded, Remaining: time.Hour})
	if who != "Server unreachable" {
		t.Errorf("degraded who = %q", who)
	}
	if policies != "" {
		t.Errorf("degraded policies = %q, want empty", policies)
	}
	if expires != "Showing the last known session" {
		t.Errorf("degraded expires = %q", expires)
	}

	if _, _, expires = MenuLines(bao.Status{State: bao.StateSignedIn, NeverExpires: true}); expires != "Never expires" {
		t.Errorf("non-expiring expires = %q", expires)
	}

	if _, policies, _ = MenuLines(bao.Status{State: bao.StateSignedIn, Name: "x"}); policies != "Policies: none" {
		t.Errorf("no policies = %q", policies)
	}
}

// Every state needs its own icon: on Windows it is the only signal.
func TestEveryStateHasADistinctIcon(t *testing.T) {
	seen := map[string]bao.State{}
	for _, s := range []bao.State{bao.StateSignedIn, bao.StateExpiring, bao.StateSignedOut, bao.StateDegraded} {
		b := Icon(s)
		if len(b) == 0 {
			t.Fatalf("Icon(%v) is empty", s)
		}
		if prev, dup := seen[string(b)]; dup {
			t.Errorf("Icon(%v) is identical to Icon(%v)", s, prev)
		}
		seen[string(b)] = s
	}
}
