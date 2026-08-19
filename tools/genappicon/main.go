// Command genappicon derives every packaged icon from one master PNG.
// Run with `go run ./tools/genappicon`.
//
// The master is committed artwork, not something this tool renders. The design
// source is an SVG, and rasterising it here would mean either a cgo dependency
// or a pure-Go tracer — and the pure-Go tracers ignore the CSS-class fills that
// Illustrator exports, so they would silently produce an unfilled shape. The
// master is exported from the SVG by tools/mkmaster.py instead; this tool owns
// only the derivation, which is deterministic and testable.
package main

import (
	"flag"
	"image"
	_ "image/png"
	"log"
	"os"
	"path/filepath"
)

func main() {
	in := flag.String("in", "assets/icon/baobar-1024.png", "master PNG")
	out := flag.String("out", "assets/icon", "output directory")
	flag.Parse()

	f, err := os.Open(*in)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	m, _, err := image.Decode(f)
	if err != nil {
		log.Fatalf("decode %s: %v", *in, err)
	}
	if err := generate(m, filepath.Dir(*in), *out); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote icons to %s", *out)
}
