package tray

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/brutalsystems/baobar/internal/bao"
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
		"signed in":    Icon(bao.StateSignedIn),
		"expiring":     Icon(bao.StateExpiring),
		"signed out":   Icon(bao.StateSignedOut),
		"degraded":     Icon(bao.StateDegraded),
		"config error": IconConfigError(),
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
		"signed in":    Icon(bao.StateSignedIn),
		"expiring":     Icon(bao.StateExpiring),
		"signed out":   Icon(bao.StateSignedOut),
		"degraded":     Icon(bao.StateDegraded),
		"config error": IconConfigError(),
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
		"signed in":    Icon(bao.StateSignedIn),
		"config error": IconConfigError(),
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
