package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/BrutalSystems/baobar/internal/bao"
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
		{"signed in", bao.Status{State: bao.StateSignedIn, Remaining: 6*time.Hour + 19*time.Minute}, "6h19m"},
		{"expiring", bao.Status{State: bao.StateExpiring, Remaining: 22 * time.Minute}, "22m"},
		{"signed out", bao.Status{State: bao.StateSignedOut}, "login"},
		{"degraded with a cached countdown", bao.Status{State: bao.StateDegraded, Remaining: 6 * time.Hour}, "~6h00m"},
		{"degraded with nothing cached", bao.Status{State: bao.StateDegraded}, "?"},
		{"non-expiring", bao.Status{State: bao.StateSignedIn, NeverExpires: true}, "∞"},
		{"degraded and non-expiring", bao.Status{State: bao.StateDegraded, NeverExpires: true}, "~∞"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Label(tc.in); got != tc.want {
				t.Errorf("Label() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The label must carry no state emoji: Icon() is the state signal, and on macOS
// both render, which put the state on screen twice. Observed on a real menu bar.
func TestLabelCarriesNoStateEmoji(t *testing.T) {
	states := []bao.Status{
		{State: bao.StateSignedIn, Remaining: time.Hour},
		{State: bao.StateExpiring, Remaining: 22 * time.Minute},
		{State: bao.StateSignedOut},
		{State: bao.StateDegraded, Remaining: time.Hour},
		{State: bao.StateDegraded},
		{State: bao.StateSignedIn, NeverExpires: true},
	}
	for _, emoji := range []string{"🔓", "🟠", "🔒", "🌐", "⚠️"} {
		for _, s := range states {
			if got := Label(s); strings.Contains(got, emoji) {
				t.Errorf("Label(%v) = %q, must not contain the state emoji %q", s.State, got, emoji)
			}
		}
	}
}

// Degraded must stay distinguishable from signed-in in TEXT, not by icon colour
// alone — the icons differ only by hue, which excludes colourblind users.
func TestDegradedIsDistinguishableWithoutColour(t *testing.T) {
	signedIn := Label(bao.Status{State: bao.StateSignedIn, Remaining: 6 * time.Hour})
	degraded := Label(bao.Status{State: bao.StateDegraded, Remaining: 6 * time.Hour})
	if signedIn == degraded {
		t.Errorf("signed-in and degraded both render as %q; the state would be visible only as icon colour", signedIn)
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
	// The row is a summary now; the names live in a submenu built from
	// policySlots. See TestPolicyLabelSummarisesRatherThanListing.
	if policies != "Policies (2)" {
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

// Every state needs its own icon: on Windows it is the only signal. This
// includes the config-error icon, which must be distinct from all four token
// states — most importantly from signed-out, since it is the icon that could
// otherwise most easily be reused for it by mistake.
func TestEveryStateHasADistinctIcon(t *testing.T) {
	seen := map[string]string{}
	check := func(name string, b []byte) {
		t.Helper()
		if len(b) == 0 {
			t.Fatalf("Icon(%s) is empty", name)
		}
		if prev, dup := seen[string(b)]; dup {
			t.Errorf("Icon(%s) is identical to Icon(%s)", name, prev)
		}
		seen[string(b)] = name
	}

	for _, s := range []bao.State{bao.StateSignedIn, bao.StateExpiring, bao.StateSignedOut, bao.StateDegraded} {
		check(s.String(), Icon(s))
	}
	check("config-error", IconConfigError())
}

// The policy list used to be joined into one menu row. With seven policies
// that row was wider than the rest of the menu put together, and it grew
// without bound as policies were added — a native menu item cannot wrap.
// It is now a submenu, so the row itself has to stay a fixed-width summary.
func TestPolicyLabelSummarisesRatherThanListing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		policies []string
		want     string
	}{
		{"none", nil, "Policies: none"},
		{"one", []string{"admin"}, "Policies (1)"},
		{"several", []string{"admin", "sops-infra", "sops-mike"}, "Policies (3)"},
	} {
		if got := PolicyLabel(tc.policies); got != tc.want {
			t.Errorf("%s: PolicyLabel = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The submenu is a fixed pool of items — systray cannot grow its item set
// freely — so the last slot has to absorb any overflow rather than silently
// dropping policies the user actually holds.
func TestPolicySlotsFillsThePoolAndReportsOverflow(t *testing.T) {
	three := []string{"admin", "sops-infra", "sops-mike"}
	if got := policySlots(three, 16); len(got) != 3 || got[0] != "admin" || got[2] != "sops-mike" {
		t.Errorf("under the cap: got %v, want the three names unchanged", got)
	}

	// Exactly at the cap: every slot is a real policy, no overflow row.
	exact := make([]string, 4)
	for i := range exact {
		exact[i] = "p" + string(rune('a'+i))
	}
	if got := policySlots(exact, 4); len(got) != 4 || got[3] != "pd" {
		t.Errorf("at the cap: got %v, want all four names", got)
	}

	// Over the cap: the last slot counts what did not fit.
	over := make([]string, 10)
	for i := range over {
		over[i] = "p" + string(rune('a'+i))
	}
	got := policySlots(over, 4)
	if len(got) != 4 {
		t.Fatalf("over the cap: got %d slots, want 4", len(got))
	}
	if got[3] != "+7 more" {
		t.Errorf("overflow slot = %q, want %q (3 names shown, 7 hidden)", got[3], "+7 more")
	}
	if got[2] != "pc" {
		t.Errorf("slot 2 = %q, want the third policy", got[2])
	}
}

func TestPolicySlotsIsEmptyForNoPolicies(t *testing.T) {
	if got := policySlots(nil, 16); len(got) != 0 {
		t.Errorf("policySlots(nil) = %v, want empty", got)
	}
}
