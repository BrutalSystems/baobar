package tray

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/BrutalSystems/baobar/internal/bao"
)

// silhouette reduces an icon to which pixels are inked, discarding colour
// entirely. Two icons with identical silhouettes are distinguishable only by
// hue — which is exactly what this package must not rely on.
func silhouette(t *testing.T, name string, raw []byte) string {
	t.Helper()

	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	b := img.Bounds()

	var mask bytes.Buffer
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0x7fff {
				mask.WriteByte('#')
			} else {
				mask.WriteByte('.')
			}
		}
	}
	return mask.String()
}

// On Windows the tray shows no text, so the icon is the ENTIRE state signal.
// Distinguishing states by colour alone excludes roughly 8% of men — and
// red-vs-green, the natural choice for signed-out vs signed-in, is the single
// worst pair for it. Every state must therefore differ in SHAPE.
func TestIconsAreDistinguishableWithoutColour(t *testing.T) {
	icons := map[string][]byte{
		"signed in":    pngFor(bao.StateSignedIn),
		"expiring":     pngFor(bao.StateExpiring),
		"signed out":   pngFor(bao.StateSignedOut),
		"degraded":     pngFor(bao.StateDegraded),
		"config error": pngConfigError,
	}

	seen := map[string]string{} // silhouette -> the state that produced it
	for name, raw := range icons {
		s := silhouette(t, name, raw)
		if prev, dup := seen[s]; dup {
			t.Errorf("%q and %q have identical silhouettes: they differ only by colour, "+
				"so on Windows (icon-only) and for colourblind users they are the same icon",
				name, prev)
		}
		seen[s] = name
	}
}

// A silhouette that is empty, or that fills the whole canvas, is not a shape.
func TestIconsHaveAMeaningfulSilhouette(t *testing.T) {
	for name, raw := range map[string][]byte{
		"signed in":    pngFor(bao.StateSignedIn),
		"expiring":     pngFor(bao.StateExpiring),
		"signed out":   pngFor(bao.StateSignedOut),
		"degraded":     pngFor(bao.StateDegraded),
		"config error": pngConfigError,
	} {
		s := silhouette(t, name, raw)
		inked := bytes.Count([]byte(s), []byte("#"))
		total := len(s)

		if inked == 0 {
			t.Errorf("%s: silhouette is empty — the icon would be invisible", name)
		}
		if inked == total {
			t.Errorf("%s: silhouette fills the whole canvas — it has no discernible shape", name)
		}
	}
}

func TestIconsAreSquareAndTraySized(t *testing.T) {
	for name, raw := range map[string][]byte{
		"signed in":    pngFor(bao.StateSignedIn),
		"config error": pngConfigError,
	} {
		cfg, err := png.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("decode config %s: %v", name, err)
		}
		if cfg.Width != cfg.Height {
			t.Errorf("%s: %dx%d is not square", name, cfg.Width, cfg.Height)
		}
		if cfg.Width < 16 || cfg.Width > 64 {
			t.Errorf("%s: %dpx is outside a sensible tray-icon range", name, cfg.Width)
		}
	}
}

// systray accepts .ico on Windows and .ico/.jpg/.png on macOS and Linux.
// Feeding it PNG on Windows produces no tray icon at all — and because
// SetTitle is a no-op there, the icon is the entire signal, so the app
// becomes invisible rather than merely ugly. Observed on a real Windows
// Server 2022 desktop: the menu opened, the process ran, and nothing
// appeared in the notification area.
func TestWindowsIconsAreICOFormat(t *testing.T) {
	// ICO header: reserved=0, type=1 (icon). PNG would start 0x89 'P' 'N' 'G'.
	isICO := func(b []byte) bool {
		return len(b) >= 6 && b[0] == 0 && b[1] == 0 && b[2] == 1 && b[3] == 0
	}

	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"signedin", icoFor(bao.StateSignedIn)},
		{"expiring", icoFor(bao.StateExpiring)},
		{"degraded", icoFor(bao.StateDegraded)},
		{"signedout", icoFor(bao.StateSignedOut)},
		{"configerror", icoConfigError},
	} {
		if len(tc.raw) == 0 {
			t.Errorf("%s: no ICO bytes embedded", tc.name)
			continue
		}
		if !isICO(tc.raw) {
			t.Errorf("%s: not ICO format (first bytes % x)", tc.name, tc.raw[:min(6, len(tc.raw))])
		}
	}
}

// The non-Windows assets must stay PNG: that is what the silhouette tests
// decode, and what macOS and Linux are given.
func TestNonWindowsIconsRemainPNG(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"signedin", pngFor(bao.StateSignedIn)},
		{"configerror", pngConfigError},
	} {
		if _, err := png.Decode(bytes.NewReader(tc.raw)); err != nil {
			t.Errorf("%s: not decodable as PNG: %v", tc.name, err)
		}
	}
}
