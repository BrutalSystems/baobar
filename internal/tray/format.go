// Package tray renders bao.Status into a system tray icon and menu. It holds no
// logic of its own: everything decidable lives in internal/bao.
package tray

import (
	"fmt"
	"strings"
	"time"

	"github.com/BrutalSystems/baobar/internal/bao"
)

// Human formats a countdown as 6h19m or 22m, never negative.
func Human(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d / time.Hour)
	m := int(d/time.Minute) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// Label is the menu bar text. macOS and Linux only — Windows shows no title.
//
// It carries NO state emoji. An earlier version prefixed one (🔓/🟠/🔒/🌐), which
// was inherited from the shell prototype that had no icon to work with. Once
// Icon() existed for Windows' sake, macOS rendered both and the state appeared
// twice side by side. The icon is the state signal; this is just the number.
//
// Degraded is marked with a "~" prefix rather than a colour, so "we cannot
// currently confirm this" survives for anyone who cannot distinguish the grey
// icon from the green one.
func Label(s bao.Status) string {
	switch s.State {
	case bao.StateSignedIn:
		if s.NeverExpires {
			return "∞"
		}
		return Human(s.Remaining)
	case bao.StateExpiring:
		return Human(s.Remaining)
	case bao.StateDegraded:
		if s.NeverExpires {
			return "~∞"
		}
		if s.Remaining <= 0 {
			return "?"
		}
		return "~" + Human(s.Remaining)
	default:
		return "login"
	}
}

// Tooltip is the whole status in one line. This is the primary readout on
// Windows, so it must stand alone without the label.
func Tooltip(s bao.Status) string {
	switch s.State {
	case bao.StateSignedIn, bao.StateExpiring:
		if s.NeverExpires {
			return fmt.Sprintf("OpenBao: signed in as %s, session does not expire%s",
				s.Name, policySuffix(s.Policies))
		}
		return fmt.Sprintf("OpenBao: signed in as %s, %s left%s",
			s.Name, Human(s.Remaining), policySuffix(s.Policies))
	case bao.StateDegraded:
		if s.NeverExpires {
			return "OpenBao: server unreachable — cached session does not expire"
		}
		if s.Remaining > 0 {
			return fmt.Sprintf("OpenBao: server unreachable — %s left on the cached session", Human(s.Remaining))
		}
		return "OpenBao: server unreachable, session unknown"
	default:
		return "OpenBao: Not signed in"
	}
}

func policySuffix(policies []string) string {
	if len(policies) == 0 {
		return ""
	}
	return " [" + strings.Join(policies, ", ") + "]"
}

// MenuLines returns the three informational rows of the menu. An empty string
// means the row should be hidden. This is the menu's last piece of judgement,
// kept here so tray.go stays pure wiring.
func MenuLines(s bao.Status) (who, policies, expires string) {
	switch s.State {
	case bao.StateSignedIn, bao.StateExpiring:
		who = "Signed in as " + s.Name
		policies = PolicyLabel(s.Policies)
		if s.NeverExpires {
			expires = "Never expires"
		} else {
			expires = "Expires in " + Human(s.Remaining)
		}
	case bao.StateDegraded:
		who = "Server unreachable"
		expires = "Showing the last known session"
	default:
		who = "Not signed in"
	}
	return who, policies, expires
}

// PolicyLabel is the menu row that opens the policy submenu. It summarises
// rather than lists: a native menu item cannot wrap, so joining the names put
// a row in the menu that grew without bound and, at seven policies, was wider
// than everything else combined.
func PolicyLabel(policies []string) string {
	if len(policies) == 0 {
		return "Policies: none"
	}
	return fmt.Sprintf("Policies (%d)", len(policies))
}

// PolicySlots is the maximum number of submenu items reserved for policies.
// The pool is fixed because systray cannot grow its item set freely, so there
// has to be a number; 16 is well clear of realistic policy counts, and going
// over degrades to a count rather than dropping names silently.
const PolicySlots = 16

// policySlots returns the titles for the submenu pool. When there are more
// policies than slots, the final slot reports how many are not shown — the
// alternative is truncating without telling the user, which for an
// authorisation list is the wrong failure.
func policySlots(policies []string, max int) []string {
	if len(policies) == 0 {
		return nil
	}
	if len(policies) <= max {
		return append([]string(nil), policies...)
	}
	out := append([]string(nil), policies[:max-1]...)
	return append(out, fmt.Sprintf("+%d more", len(policies)-(max-1)))
}
