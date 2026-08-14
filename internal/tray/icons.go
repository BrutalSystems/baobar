package tray

import (
	_ "embed"

	"github.com/brutalsystems/baobar/internal/bao"
)

// Icons are embedded so the binary has no runtime asset dependency. Regenerate
// with `go run ./tools/genicons`.
var (
	//go:embed assets/signedin.png
	iconSignedIn []byte
	//go:embed assets/expiring.png
	iconExpiring []byte
	//go:embed assets/signedout.png
	iconSignedOut []byte
	//go:embed assets/degraded.png
	iconDegraded []byte
)

// Icon returns the tray image for a state. On Windows this is the only signal
// the user gets — there is no text label in the tray — so the four must differ
// at a glance, not merely differ.
func Icon(s bao.State) []byte {
	switch s {
	case bao.StateSignedIn:
		return iconSignedIn
	case bao.StateExpiring:
		return iconExpiring
	case bao.StateDegraded:
		return iconDegraded
	default:
		return iconSignedOut
	}
}
