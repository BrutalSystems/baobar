package tray

import (
	_ "embed"

	"github.com/BrutalSystems/baobar/internal/bao"
)

// Icons are embedded so the binary has no runtime asset dependency. Regenerate
// with `go run ./tools/genicons`.
// Both formats are embedded on every platform. systray takes .ico on Windows
// and .ico/.jpg/.png on macOS and Linux, and picking at build time would hide
// the unused set from the tests — the ICO assets need checking wherever the
// suite runs, not only on Windows. The whole second set is under a kilobyte.
var (
	//go:embed assets/signedin.png
	pngSignedIn []byte
	//go:embed assets/expiring.png
	pngExpiring []byte
	//go:embed assets/signedout.png
	pngSignedOut []byte
	//go:embed assets/degraded.png
	pngDegraded []byte
	//go:embed assets/configerror.png
	pngConfigError []byte

	//go:embed assets/signedin.ico
	icoSignedIn []byte
	//go:embed assets/expiring.ico
	icoExpiring []byte
	//go:embed assets/signedout.ico
	icoSignedOut []byte
	//go:embed assets/degraded.ico
	icoDegraded []byte
	//go:embed assets/configerror.ico
	icoConfigError []byte
)

func pngFor(s bao.State) []byte {
	switch s {
	case bao.StateSignedIn:
		return pngSignedIn
	case bao.StateExpiring:
		return pngExpiring
	case bao.StateDegraded:
		return pngDegraded
	default:
		return pngSignedOut
	}
}

func icoFor(s bao.State) []byte {
	switch s {
	case bao.StateSignedIn:
		return icoSignedIn
	case bao.StateExpiring:
		return icoExpiring
	case bao.StateDegraded:
		return icoDegraded
	default:
		return icoSignedOut
	}
}

// Icon returns the tray image for a state. On Windows this is the only signal
// the user gets — there is no text label in the tray — so the four must differ
// at a glance, not merely differ.
func Icon(s bao.State) []byte {
	if useICO {
		return icoFor(s)
	}
	return pngFor(s)
}

// IconConfigError is the misconfiguration-state icon: a filled triangle,
// distinct by SHAPE (not merely a fifth hue) from the four circular state
// icons above, so a misconfigured Baobar cannot be mistaken for a signed-out
// one on Windows, where the icon plus tooltip is the whole signal.
func IconConfigError() []byte {
	if useICO {
		return icoConfigError
	}
	return pngConfigError
}
