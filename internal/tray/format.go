// Package tray renders bao.Status into a system tray icon and menu. It holds no
// logic of its own: everything decidable lives in internal/bao.
package tray

import (
	"fmt"
	"strings"
	"time"

	"github.com/brutalsystems/baobar/internal/bao"
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
func Label(s bao.Status) string {
	switch s.State {
	case bao.StateSignedIn:
		if s.NeverExpires {
			return "🔓 ∞"
		}
		return "🔓 " + Human(s.Remaining)
	case bao.StateExpiring:
		return "🟠 " + Human(s.Remaining)
	case bao.StateDegraded:
		if s.NeverExpires {
			return "🌐 ∞"
		}
		if s.Remaining <= 0 {
			return "🌐 ?"
		}
		return "🌐 " + Human(s.Remaining)
	default:
		return "🔒 login"
	}
}

// Tooltip is the whole status in one line. This is the primary readout on
// Windows, so it must stand alone without the label.
func Tooltip(s bao.Status) string {
	switch s.State {
	case bao.StateSignedIn, bao.StateExpiring:
		return fmt.Sprintf("OpenBao: signed in as %s, %s left%s",
			s.Name, Human(s.Remaining), policySuffix(s.Policies))
	case bao.StateDegraded:
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
		policies = "Policies: " + policyList(s.Policies)
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

func policyList(policies []string) string {
	if len(policies) == 0 {
		return "none"
	}
	return strings.Join(policies, ", ")
}
