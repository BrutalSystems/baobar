package main

import (
	"os"
	"path/filepath"
	"testing"

	ico "github.com/sergeymakinen/go-ico"
)

// TestCommittedICOHasEverySize guards the checked-in artwork, which no Go code
// imports: the .ico is consumed only by the release pipeline, so a truncated,
// stale or wrongly regenerated file would otherwise be caught for the first
// time by goversioninfo, in CI, during a release.
func TestCommittedICOHasEverySize(t *testing.T) {
	path := filepath.Join("..", "..", "assets", "icon", "baobar.ico")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		t.Skip("app icon not generated yet")
	}
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	imgs, err := ico.DecodeAll(f)
	if err != nil {
		t.Fatalf("the committed .ico does not decode: %v", err)
	}
	if len(imgs) != len(icoSizes) {
		t.Errorf("the committed .ico holds %d images, want %d", len(imgs), len(icoSizes))
	}
}
