package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	ico "github.com/sergeymakinen/go-ico"
)

// master returns a synthetic square icon: a light field with a dark centre
// block. The contrast matters — a resize bug that yields a uniform image would
// pass a "file exists and is square" check, so the fixture is built so that a
// correct downscale still has both colours in it.
func master(n int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{0xfe, 0xfe, 0xfe, 0xff}}, image.Point{}, draw.Src)
	inset := n / 4
	draw.Draw(img, image.Rect(inset, inset, n-inset, n-inset),
		&image.Uniform{color.RGBA{0x05, 0x0f, 0x1e, 0xff}}, image.Point{}, draw.Src)
	return img
}

func TestCheckMasterRejectsNonSquare(t *testing.T) {
	if err := checkMaster(image.NewRGBA(image.Rect(0, 0, 1024, 512))); err == nil {
		t.Fatal("checkMaster accepted a non-square master")
	}
}

func TestCheckMasterRejectsTooSmall(t *testing.T) {
	if err := checkMaster(master(512)); err == nil {
		t.Fatal("checkMaster accepted a 512px master; 1024 is the minimum")
	}
}

func TestCheckMasterAcceptsSquare1024(t *testing.T) {
	if err := checkMaster(master(1024)); err != nil {
		t.Fatalf("checkMaster rejected a valid master: %v", err)
	}
}

func TestSizedPrefersACommittedOverride(t *testing.T) {
	src := t.TempDir()
	// A 16px override that cannot be confused with a downscale of the fixture.
	override := image.NewRGBA(image.Rect(0, 0, 16, 16))
	draw.Draw(override, override.Bounds(), &image.Uniform{color.RGBA{0xff, 0x00, 0x00, 0xff}}, image.Point{}, draw.Src)
	f, err := os.Create(filepath.Join(src, "baobar-16.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, override); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got := sized(master(1024), 16, src)
	if r, _, _, _ := got.At(8, 8).RGBA(); r>>8 != 0xff {
		t.Errorf("sized ignored the committed 16px override")
	}
	// A size with no override still comes from the master.
	if r, _, _, _ := sized(master(1024), 48, src).At(24, 24).RGBA(); r>>8 == 0xff {
		t.Errorf("sized used the 16px override for the 48px image")
	}
}

func TestSizedRejectsAMisSizedOverride(t *testing.T) {
	src := t.TempDir()
	wrong := image.NewRGBA(image.Rect(0, 0, 24, 24))
	f, err := os.Create(filepath.Join(src, "baobar-16.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, wrong); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// A 24px file named -16 is a mistake, not an instruction: fall back to the
	// master rather than emitting an icon of the wrong size.
	if got := sized(master(1024), 16, src); got.Bounds().Dx() != 16 {
		t.Errorf("sized returned a %dpx image for size 16", got.Bounds().Dx())
	}
}

func TestGenerateWritesEveryICOSize(t *testing.T) {
	dir := t.TempDir()
	if err := generate(master(1024), dir, dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	f, err := os.Open(filepath.Join(dir, "baobar.ico"))
	if err != nil {
		t.Fatalf("open ico: %v", err)
	}
	defer f.Close()
	imgs, err := ico.DecodeAll(f)
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}
	if len(imgs) != len(icoSizes) {
		t.Fatalf("ico holds %d images, want %d", len(imgs), len(icoSizes))
	}
	got := map[int]bool{}
	for _, im := range imgs {
		got[im.Bounds().Dx()] = true
	}
	for _, n := range icoSizes {
		if !got[int(n)] {
			t.Errorf("ico is missing the %dpx image", n)
		}
	}
}

func TestGenerateWritesICNS(t *testing.T) {
	dir := t.TempDir()
	if err := generate(master(1024), dir, dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "baobar.icns"))
	if err != nil {
		t.Fatalf("read icns: %v", err)
	}
	if !bytes.HasPrefix(b, []byte("icns")) {
		t.Error("icns file does not start with the icns magic")
	}
}

func TestGenerateWritesHicolorTree(t *testing.T) {
	dir := t.TempDir()
	if err := generate(master(1024), dir, dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, n := range hicolorSizes {
		p := filepath.Join(dir, "hicolor", fmt.Sprintf("%dx%d", n, n), "apps", "baobar.png")
		f, err := os.Open(p)
		if err != nil {
			t.Fatalf("open %s: %v", p, err)
		}
		cfg, err := png.DecodeConfig(f)
		f.Close()
		if err != nil {
			t.Fatalf("decode %s: %v", p, err)
		}
		if cfg.Width != int(n) || cfg.Height != int(n) {
			t.Errorf("%s is %dx%d, want %dx%d", p, cfg.Width, cfg.Height, n, n)
		}
	}
}
