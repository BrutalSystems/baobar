// Command genicons writes Baobar's tray icons: one per state, each a distinct
// SHAPE as well as a distinct colour. Run with `go run ./tools/genicons`.
//
// Shape matters more than colour here. On Windows the tray shows no text at
// all, so the icon is the entire state signal — and roughly 8% of men cannot
// reliably separate the red/green pair that would otherwise carry "signed out"
// versus "signed in". Every icon below is therefore identifiable in greyscale:
//
//	signed in    filled circle
//	expiring     filled diamond
//	signed out   thick ring (hollow)
//	degraded     hollow square
//	config error filled triangle
//
// internal/tray's icon test decodes these and compares their alpha masks, so a
// future change that makes two states differ only by hue fails the build.
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
)

const (
	size = 22
	c    = size / 2 // centre
	r    = 9        // outer radius / half-extent
)

// shape reports whether the pixel at (x, y) is inked.
type shape func(dx, dy int) bool

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var icons = []struct {
	name  string
	fill  color.RGBA
	shape shape
}{
	{"signedin", color.RGBA{0x3b, 0xa5, 0x5d, 0xff}, func(dx, dy int) bool {
		return dx*dx+dy*dy <= r*r // filled circle
	}},
	{"expiring", color.RGBA{0xe0, 0x8b, 0x1f, 0xff}, func(dx, dy int) bool {
		return abs(dx)+abs(dy) <= r // filled diamond
	}},
	{"signedout", color.RGBA{0xc0, 0x39, 0x2b, 0xff}, func(dx, dy int) bool {
		d := dx*dx + dy*dy
		return d <= r*r && d > (r-4)*(r-4) // thick ring
	}},
	{"degraded", color.RGBA{0x8a, 0x8a, 0x8a, 0xff}, func(dx, dy int) bool {
		m := max(abs(dx), abs(dy))
		return m <= r && m > r-3 // hollow square
	}},
	{"configerror", color.RGBA{0x6a, 0x3d, 0x9a, 0xff}, func(dx, dy int) bool {
		// Filled triangle, apex up: half-width grows linearly with depth.
		if dy < -r || dy > r-1 {
			return false
		}
		half := (dy + r) * r / (2 * r)
		return abs(dx) <= half
	}},
}

func main() {
	dir := filepath.Join("internal", "tray", "assets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}

	for _, ic := range icons {
		img := image.NewRGBA(image.Rect(0, 0, size, size))
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				if ic.shape(x-c, y-c) {
					img.Set(x, y, ic.fill)
				}
			}
		}

		f, err := os.Create(filepath.Join(dir, ic.name+".png"))
		if err != nil {
			log.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			log.Fatal(err)
		}
		if err := f.Close(); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("wrote %d icons to %s", len(icons), dir)
}
