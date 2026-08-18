package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/jackmordaunt/icns/v3"
	"github.com/nfnt/resize"
	ico "github.com/sergeymakinen/go-ico"
)

// icoSizes are packed into the multi-image .ico embedded in the Windows exe.
// Explorer picks per view — 16 for the details list, 32 for the taskbar, 256
// for extra-large icons — and scales one itself if the size it wants is absent,
// which looks worse than a purpose-built raster at each step.
var icoSizes = []uint{256, 128, 64, 48, 32, 16}

// hicolorSizes are the freedesktop icon theme sizes.
var hicolorSizes = []uint{512, 256, 128, 64, 48, 32, 16}

// minMaster is the smallest master that fills every size above without
// upscaling. The check is worth having because icns.Encode derives its own set
// from whatever it is handed: a small master yields a small iconset silently
// rather than failing.
const minMaster = 1024

func checkMaster(img image.Image) error {
	b := img.Bounds()
	if b.Dx() != b.Dy() {
		return fmt.Errorf("master must be square, got %dx%d", b.Dx(), b.Dy())
	}
	if b.Dx() < minMaster {
		return fmt.Errorf("master must be at least %dx%d, got %dx%d", minMaster, minMaster, b.Dx(), b.Dy())
	}
	return nil
}

func scale(img image.Image, n uint) image.Image {
	return resize.Resize(n, n, img, resize.Lanczos3)
}

// sized returns the art for one output size: a committed baobar-<n>.png from
// srcDir when one exists, otherwise a downscale of the master.
//
// The overrides exist because one inset cannot serve every size. The mark is
// inset from the tile edge, and the 10% that reads well at 1024 wastes pixels
// at 16, where the bun ends up small inside its own tile. tools/mkmaster.py
// composes 16 and 32 with the mark spanning more of the tile.
//
// A file of the wrong dimensions is treated as a mistake rather than an
// instruction: emitting a 24px image where a 16px one was asked for would
// produce a malformed .ico that only fails much later.
func sized(m image.Image, n uint, srcDir string) image.Image {
	f, err := os.Open(filepath.Join(srcDir, fmt.Sprintf("baobar-%d.png", n)))
	if err != nil {
		return scale(m, n)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		return scale(m, n)
	}
	b := img.Bounds()
	if b.Dx() != int(n) || b.Dy() != int(n) {
		return scale(m, n)
	}
	return img
}

// writeFile creates path and its parent directory, then hands the file to fn.
// The close error is returned when fn itself succeeded, so a failed flush is
// not silently discarded.
func writeFile(path string, fn func(*os.File) error) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	return fn(f)
}

func writeICO(m image.Image, srcDir, path string) error {
	imgs := make([]image.Image, 0, len(icoSizes))
	for _, n := range icoSizes {
		imgs = append(imgs, sized(m, n, srcDir))
	}
	return writeFile(path, func(f *os.File) error { return ico.EncodeAll(f, imgs) })
}

func writeICNS(m image.Image, path string) error {
	return writeFile(path, func(f *os.File) error { return icns.Encode(f, m) })
}

func writeHicolor(m image.Image, srcDir, outDir string) error {
	for _, n := range hicolorSizes {
		img := sized(m, n, srcDir)
		p := filepath.Join(outDir, "hicolor", fmt.Sprintf("%dx%d", n, n), "apps", "baobar.png")
		if err := writeFile(p, func(f *os.File) error { return png.Encode(f, img) }); err != nil {
			return err
		}
	}
	return nil
}

// generate writes every derived asset into outDir, taking per-size art from
// srcDir where a committed override exists.
//
// Note that the .icns gets none of them: icns.Encode derives its own set from
// the single image it is handed, so macOS small sizes come from the master.
// Acceptable — macOS never renders this icon as small as Explorer's 16px list
// view does, which is the case the overrides exist for.
func generate(m image.Image, srcDir, outDir string) error {
	if err := checkMaster(m); err != nil {
		return err
	}
	if err := writeICO(m, srcDir, filepath.Join(outDir, "baobar.ico")); err != nil {
		return err
	}
	if err := writeICNS(m, filepath.Join(outDir, "baobar.icns")); err != nil {
		return err
	}
	return writeHicolor(m, srcDir, outDir)
}
