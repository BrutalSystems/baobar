// Command genicons writes Baobar's placeholder tray icons: a filled circle per
// state. Run with `go run ./tools/genicons`. Replace with real artwork before
// release (see the design doc's open question 4).
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

func main() {
	states := map[string]color.RGBA{
		"signedin": {0x3b, 0xa5, 0x5d, 0xff}, // green
		"expiring": {0xe0, 0x8b, 0x1f, 0xff}, // amber
		"signedout": {0xc0, 0x39, 0x2b, 0xff}, // red
		"degraded": {0x8a, 0x8a, 0x8a, 0xff}, // grey
	}
	dir := filepath.Join("internal", "tray", "assets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}

	for name, c := range states {
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

		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			log.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			log.Fatal(err)
		}
		f.Close()
	}
	log.Printf("wrote 4 icons to %s", dir)
}
