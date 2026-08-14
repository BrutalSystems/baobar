// Command genicons writes Baobar's placeholder tray icons: a filled circle
// per state, plus a filled triangle for the config-error state. Run with
// `go run ./tools/genicons`. Replace with real artwork before release (see
// the design doc's open question 4).
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
)

const size = 22

// circles are the four token-state icons. They differ only by hue, which is
// a known accessibility weakness — colorblind users can't rely on this alone.
var circles = map[string]color.RGBA{
	"signedin":  {0x3b, 0xa5, 0x5d, 0xff}, // green
	"expiring":  {0xe0, 0x8b, 0x1f, 0xff}, // amber
	"signedout": {0xc0, 0x39, 0x2b, 0xff}, // red
	"degraded":  {0x8a, 0x8a, 0x8a, 0xff}, // grey
}

// triangleColor is the config-error icon. It is a different SHAPE from the
// four circles above, not merely a fifth hue, so a misconfigured Baobar
// doesn't read as just another token state on Windows, where the tooltip is
// the only text and the icon carries the rest. The color (purple) is also
// distinct from all four circle hues, but shape is the deliberate signal.
var triangleColor = color.RGBA{0x6a, 0x3d, 0x9a, 0xff} // purple

func main() {
	dir := filepath.Join("internal", "tray", "assets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}

	for name, c := range circles {
		write(dir, name, drawCircle(c))
	}
	write(dir, "configerror", drawTriangle(triangleColor))

	log.Printf("wrote %d icons to %s", len(circles)+1, dir)
}

func drawCircle(c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	const r = size/2 - 2
	cx, cy := size/2, size/2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= r*r {
				img.Set(x, y, c)
			}
		}
	}
	return img
}

// drawTriangle fills an upward-pointing equilateral-ish triangle inscribed in
// the icon square, using the same half-pixel inset as drawCircle so the two
// shapes read as comparably sized at tray resolution.
func drawTriangle(c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	const inset = 2
	top, bottom := inset, size-1-inset
	height := bottom - top
	for y := top; y <= bottom; y++ {
		// Linear taper from a single point at the top to full width at the
		// bottom: classic scanline triangle fill.
		frac := float64(y-top) / float64(height)
		halfWidth := frac * float64(size/2-inset)
		cx := float64(size) / 2
		left := int(cx - halfWidth)
		right := int(cx + halfWidth)
		for x := left; x <= right; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func write(dir, name string, img *image.RGBA) {
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
}
