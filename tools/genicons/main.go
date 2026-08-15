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
	"bytes"
	"encoding/binary"
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

		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ic.name+".png"), buf.Bytes(), 0o644); err != nil {
			log.Fatal(err)
		}
		// Windows needs .ico: systray accepts PNG on macOS and Linux only, and
		// handing it PNG there yields no tray icon at all.
		if err := os.WriteFile(filepath.Join(dir, ic.name+".ico"), icoWrap(buf.Bytes()), 0o644); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("wrote %d icons to %s", len(icons), dir)
}

// icoWrap packages PNG bytes as a single-image .ico. Windows Vista and later
// accept PNG-compressed icon entries, so the artwork needs no re-encoding —
// only the 22-byte container that tells Windows what it is.
//
// Layout: a 6-byte ICONDIR, one 16-byte ICONDIRENTRY, then the PNG itself.
func icoWrap(pngBytes []byte) []byte {
	var b bytes.Buffer
	w16 := func(v uint16) { _ = binary.Write(&b, binary.LittleEndian, v) }
	w32 := func(v uint32) { _ = binary.Write(&b, binary.LittleEndian, v) }

	w16(0) // reserved, always 0
	w16(1) // type 1 = icon
	w16(1) // one image

	// Width and height are single bytes, where 0 means 256. size is 22, so it
	// fits directly; the constant is asserted below to keep that true.
	b.WriteByte(byte(size))
	b.WriteByte(byte(size))
	b.WriteByte(0) // palette size, 0 for non-paletted
	b.WriteByte(0) // reserved
	w16(1)         // colour planes
	w16(32)        // bits per pixel
	w32(uint32(len(pngBytes)))
	w32(6 + 16) // the image data begins after the header and one entry

	b.Write(pngBytes)
	return b.Bytes()
}
