# Desktop Integration: Icons, Windows Resources, Linux Packaging — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give Baobar a real application icon on Windows and Linux, and the launcher entries that go with it.

**Architecture:** One committed master PNG is the single source for every derived
asset. A new pure-Go tool, `tools/genappicon`, turns it into a multi-size `.ico`,
an `.icns`, and a freedesktop hicolor PNG tree; those outputs are committed, the
way `tools/genicons` already works. Windows embeds the `.ico` plus a VERSIONINFO
block into the exe via a build-time tool that never enters `go.mod`. Linux gains
`.deb`/`.rpm` packages that place a desktop entry and the icon tree at their
standard paths.

**Tech Stack:** Go 1.26.3, GoReleaser (OSS) v2, `goversioninfo` v1.7.0 (build
tool only), Chocolatey, nFPM via GoReleaser.

**Spec:** `docs/superpowers/specs/2026-08-18-desktop-integration-design.md`

## Verified During Review

These were run end to end before the plan was approved. The implementer does not
need to re-derive them, and should treat a deviation as a signal that something
changed:

- `go-ico` writes the 256px entry **PNG-compressed** and the rest as BMP;
  `goversioninfo` v1.7.0 accepts that file (`rc=0`) and emits all four
  `.syso` files, **arm64 included**.
- `-platform-specific` writes into the **working directory** and ignores `-o`,
  which is why the hook has to change directory.
- The `.syso` links: a Windows exe built with it is ~156 KB larger and contains
  the VERSIONINFO strings; the same build without it does not.
- `before.hooks` in GoReleaser OSS is a list of **strings** — the `dir:`/`cmd:`
  object form does *not* parse (`cannot unmarshal !!map into string`). The
  `sh -c "cd ... && ..."` form in Task 4 and the `nfpms` block in Task 6 both
  pass `goreleaser check`.

## Global Constraints

- **No new module dependencies.** `github.com/jackmordaunt/icns/v3`,
  `github.com/nfnt/resize` and `github.com/sergeymakinen/go-ico` are already in
  `go.sum` as indirect dependencies of `beeep` and already recorded in
  `THIRD_PARTY_LICENSES.md`. They get promoted to direct requires. Nothing else
  is added. `goversioninfo` is invoked via `go run <pkg>@v1.7.0` precisely so it
  does not enter the graph.
- **The `.syso` must never affect non-Windows builds.** `GOOS=linux go build`
  and `GOOS=darwin go build` must keep working with it present.
- **Do not touch `internal/tray/assets` or `tools/genicons`.** The five 22px
  state glyphs are a different asset with a different job, protected by a
  silhouette test. This plan does not derive them from the app icon and does not
  change them.
- **macOS is out of scope for this plan.** No `.app` bundle, no changes to the
  `notarize:` block in `.goreleaser.yaml`. The `.icns` is generated here because
  it costs one function call, but nothing consumes it yet.
- **Generated assets are committed**, not built in CI — matching `tools/genicons`.
  The one exception is the Windows `.syso`, which bakes in a version string and
  is therefore built per release and gitignored.
- Icon theme name is `baobar` (lowercase) everywhere; display name is `Baobar`.

---

### Task 1: Icon derivation tool

Builds `tools/genappicon` and its tests. Uses a synthetic image, so it is **not**
blocked on the real artwork.

**Files:**
- Create: `tools/genappicon/icons.go`
- Create: `tools/genappicon/icons_test.go`
- Create: `tools/genappicon/main.go`
- Modify: `go.mod`
- Modify: `THIRD_PARTY_LICENSES.md` (regenerated)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `checkMaster(img image.Image) error`
  - `sized(m image.Image, n uint, srcDir string) image.Image` — the committed
    override for size `n` if `srcDir/baobar-<n>.png` exists, else a downscale
  - `generate(m image.Image, srcDir, outDir string) error` — writes
    `baobar.ico`, `baobar.icns`, and `hicolor/<n>x<n>/apps/baobar.png` under
    `outDir`, taking per-size art from `srcDir`
  - `var icoSizes = []uint{256, 128, 64, 48, 32, 16}`
  - `var hicolorSizes = []uint{512, 256, 128, 64, 48, 32, 16}`

- [ ] **Step 1: Write the failing tests**

Do this before touching `go.mod`. The three modules are already in the graph as
indirect requires, so the imports below resolve immediately; `go mod tidy` in
Step 7 promotes them to direct once real code imports them. Running `go get`
first would work too, but any `go mod tidy` between then and Step 4 — including
the one in the release pipeline's `before.hooks` — would demote them straight
back, which looks like a mystery.

Create `tools/genappicon/icons_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./tools/genappicon/`
Expected: FAIL — compile error, `undefined: checkMaster`, `undefined: generate`,
`undefined: icoSizes`, `undefined: hicolorSizes`.

- [ ] **Step 3: Write the derivation logic**

Create `tools/genappicon/icons.go`:

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./tools/genappicon/ -v`
Expected: PASS, six tests.

- [ ] **Step 5: Write the command entry point**

Create `tools/genappicon/main.go`:

```go
// Command genappicon derives every packaged icon from one master PNG.
// Run with `go run ./tools/genappicon`.
//
// The master is committed artwork, not something this tool renders. The design
// source is an SVG, and rasterising it here would mean either a cgo dependency
// or a pure-Go tracer — and the pure-Go tracers ignore the CSS-class fills that
// Illustrator exports, so they would silently produce an unfilled shape. The
// master is exported from the SVG by hand instead; this tool owns only the
// derivation, which is deterministic and testable.
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
```

- [ ] **Step 6: Verify the tree builds, vets clean, and go.mod is tidy**

Run:
```bash
go mod tidy
go build ./... && go vet ./... && gofmt -l . && go test ./... -race
git diff go.mod
```
Expected: no output from `gofmt -l`, all tests pass, and `git diff go.mod` shows
the three modules losing their `// indirect` markers.

- [ ] **Step 7: Regenerate the third-party licence file**

Run: `./tools/gen-third-party-licenses.sh`
Expected: the three promoted dependencies may move between sections. If the file
is unchanged, that is fine — they were already listed.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum tools/genappicon/ THIRD_PARTY_LICENSES.md
git commit -m "feat(icons): derive app icon assets from a master PNG"
```

---

### Task 2: Icon key in the XDG autostart entry

Independent of the artwork — ship it whether or not the master PNG has arrived.

**Files:**
- Modify: `internal/autostart/render.go` (`renderDesktop`)
- Test: `internal/autostart/autostart_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing consumed by later tasks. `renderDesktop(exe string) []byte`
  keeps its signature; only its output gains a line.

- [ ] **Step 1: Write the failing test**

Add to `internal/autostart/autostart_test.go` (check that `strings` is already
imported at the top of the file; add it if not):

```go
func TestRenderDesktopIncludesIconKey(t *testing.T) {
	entry := string(renderDesktop("/usr/bin/baobar"))
	if !strings.Contains(entry, "\nIcon=baobar\n") {
		t.Errorf("autostart entry has no Icon= key:\n%s", entry)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/autostart/ -run TestRenderDesktopIncludesIconKey -v`
Expected: FAIL — the printed entry has no `Icon=` line.

- [ ] **Step 3: Add the key**

In `internal/autostart/render.go`, in `renderDesktop`, the returned template
becomes:

```go
	return []byte(fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Baobar
Icon=baobar
Exec="%s"
Terminal=false
X-GNOME-Autostart-enabled=true
`, escaped))
```

An icon *theme name*, not a path, per the Desktop Entry spec. It resolves once
the hicolor tree is installed (Task 6). For someone running an extracted tarball
it does not resolve — which is exactly what happens today, so no regression.

Note what this does *not* do: entries already written to
`~/.config/autostart/baobar.desktop` are not rewritten. `Enable()` only writes on
demand, so an existing user keeps an iconless entry until they toggle "Start at
login" off and on. That is acceptable — it is cosmetic, and rewriting entries
behind the user's back on startup would be worse — but say so in the release
notes rather than letting it look like the fix did not work.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/autostart/ -race -v`
Expected: PASS, including the pre-existing `desktopTarget` round-trip test —
that parser reads only the `Exec=` line, so the new key cannot disturb it.

- [ ] **Step 5: Commit**

```bash
git add internal/autostart/render.go internal/autostart/autostart_test.go
git commit -m "fix(autostart): give the XDG autostart entry an icon"
```

---

### Task 3: Generate and commit the real icon assets

**No longer blocked** — the artwork is committed. `assets/icon/baobar-1024.png`,
`baobar-32.png` and `baobar-16.png` were produced by `tools/mkmaster.py` from
`baobar.svg`, on a cream (`#F2E3C6`) tile with the mark spanning 80/88/92% of the
tile respectively.

**Files:**
- Already present: `assets/icon/baobar-{1024,32,16}.png`, `tools/mkmaster.py`
- Create: `assets/icon/baobar.ico`, `assets/icon/baobar.icns`,
  `assets/icon/hicolor/<n>x<n>/apps/baobar.png` (generated)

**Interfaces:**
- Consumes: `generate` from Task 1.
- Produces: `assets/icon/baobar.ico` for Task 4;
  `assets/icon/hicolor/<n>x<n>/apps/baobar.png` for Task 6.

- [ ] **Step 1: Confirm the master meets the contract**

Run: `go run ./tools/genappicon`
Expected: `wrote icons to assets/icon`. If it exits with "master must be square"
or "master must be at least 1024x1024", the artwork is wrong — stop and go back
to the designer rather than working around it.

- [ ] **Step 2: Confirm every expected file landed**

Run:
```bash
find assets/icon -type f | sort
```
Expected: `baobar.svg`, `baobar-1024.png`, `baobar.ico`, `baobar.icns`, and
seven `hicolor/<n>x<n>/apps/baobar.png` files.

- [ ] **Step 3: Look at the small sizes**

Open `assets/icon/hicolor/16x16/apps/baobar.png` and
`assets/icon/hicolor/32x32/apps/baobar.png` at 100%. This is a human judgement
and cannot be automated: if the pleat lines have blurred into the bun, the fix
is a hand-exported override from Illustrator at those two sizes, per the spec's
"Small sizes" section. Note what you see either way.

- [ ] **Step 4: Add a guard test for the committed artwork**

Nothing in the Go build references `assets/` — the `.ico` is consumed only by the
release pipeline. So a truncated, stale, or wrongly-regenerated icon would be
caught for the first time by `goversioninfo`, in CI, during a release. This test
moves that failure to `go test`.

Create `tools/genappicon/assets_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"

	ico "github.com/sergeymakinen/go-ico"
)

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
```

Run: `go test ./tools/genappicon/ -run TestCommittedICO -v`
Expected: PASS (not SKIP — the asset exists by now).

- [ ] **Step 5: Commit**

```bash
git add assets/icon tools/genappicon/assets_test.go
git commit -m "feat(icons): add the Baobar app icon and its derived assets"
```

---

### Task 4: Embed the icon and version info in the Windows exe

**Files:**
- Create: `packaging/windows/versioninfo.json`
- Modify: `.goreleaser.yaml` (the `before.hooks` block)
- Modify: `.gitignore` (it exists; currently ignores `dist/`, `/baobar`, `.DS_Store` and others)

**Interfaces:**
- Consumes: `assets/icon/baobar.ico` from Task 3.
- Produces: `cmd/baobar/resource_windows_amd64.syso` and
  `cmd/baobar/resource_windows_arm64.syso` at build time. Nothing imports them;
  the Go linker picks them up by filename.

- [ ] **Step 1: Confirm the tool's config schema before writing it**

Run:
```bash
cd /tmp && go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.7.0 -example
```
Expected: a JSON document printed to stdout. Compare its shape to the file in the
next step and prefer the tool's own output if the two disagree — this step exists
because the schema is being reproduced from documentation, not from the tool.

- [ ] **Step 2: Write the version info config**

Create `packaging/windows/versioninfo.json`:

```json
{
	"FixedFileInfo": {
		"FileVersion": {"Major": 0, "Minor": 0, "Patch": 0, "Build": 0},
		"ProductVersion": {"Major": 0, "Minor": 0, "Patch": 0, "Build": 0},
		"FileFlagsMask": "3f",
		"FileFlags": "00",
		"FileOS": "040004",
		"FileType": "01",
		"FileSubType": "00"
	},
	"StringFileInfo": {
		"CompanyName": "BrutalSystems",
		"FileDescription": "Menu bar / system tray indicator for OpenBao login state and token expiry",
		"InternalName": "baobar.exe",
		"LegalCopyright": "2026 BrutalSystems",
		"OriginalFilename": "baobar.exe",
		"ProductName": "Baobar"
	},
	"VarFileInfo": {
		"Translation": {"LangID": "0409", "CharsetID": "04B0"}
	}
}
```

The version numbers are zeros on purpose: the real values are passed on the
command line from the build, so the committed file never goes stale.

- [ ] **Step 3: Run the generator by hand and find out where it writes**

Run:
```bash
cd cmd/baobar && go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.7.0 \
  -platform-specific \
  -icon ../../assets/icon/baobar.ico \
  -ver-major 0 -ver-minor 1 -ver-patch 5 \
  ../../packaging/windows/versioninfo.json ; cd ../..
ls cmd/baobar/*.syso
```
Expected: `resource_windows_amd64.syso` and `resource_windows_arm64.syso` inside
`cmd/baobar/`. `-platform-specific` ignores `-o` and writes into the working
directory, which is why the command runs from `cmd/baobar` — Go only links
`.syso` files that sit in the `main` package's own directory. If the files land
somewhere else, adjust the working directory until they land here.

- [ ] **Step 4: Verify the resource is actually linked into the exe**

Run:
```bash
GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui" -o /tmp/baobar.exe ./cmd/baobar
LC_ALL=C tr -d '\0' < /tmp/baobar.exe > /tmp/baobar.stripped
grep -a -c BrutalSystems /tmp/baobar.stripped
```
Expected: `1`. VERSIONINFO strings are stored as UTF-16LE, so `tr` strips the
interleaved NUL bytes that would otherwise defeat `grep`.

**`LC_ALL=C` is required and the intermediate file is deliberate.** Without the
locale override, macOS `tr` aborts on binary input with "Illegal byte sequence"
and the check reports a false failure. Piping directly into `grep` also
interleaves badly with a preceding `printf`. Note that `grep -c` exits 1 when the
count is 0, so this line will abort a `set -e` script on failure — which is what
you want.

A count of 0 means the `.syso` was not linked, usually because it is in the wrong
directory. Cross-check by comparing binary sizes with and without it: the
resource adds roughly 156 KB.

- [ ] **Step 5: Verify the other two platforms are unaffected**

Run:
```bash
GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/baobar
GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -o /dev/null ./cmd/baobar
```
Expected: both succeed with the `.syso` files still present in `cmd/baobar/`.
This is the regression the filename suffixes exist to prevent; prove it rather
than assume it.

- [ ] **Step 6: Keep the generated resources out of the tree**

Append to the existing `.gitignore`:

```gitignore
# Windows resource objects: generated per release by the goreleaser before hook,
# because the version string is baked into them.
cmd/baobar/*.syso
```

- [ ] **Step 7: Wire it into the release build**

In `.goreleaser.yaml`, replace the `before` block with:

```yaml
before:
  hooks:
    - go mod tidy
    # Embeds the app icon and a VERSIONINFO block into the Windows exe. Without
    # this the binary has a blank generic icon in Explorer, the taskbar and the
    # SmartScreen dialog, and no publisher string anywhere — on a platform where
    # the icon is the entire state signal and the binary is unsigned, that is
    # the worst first impression of the three platforms.
    #
    # `go run <pkg>@version` deliberately keeps goversioninfo out of go.mod: it
    # is a build tool, not a library. `-platform-specific` writes one .syso per
    # architecture with the filename suffixes Go uses to constrain them to
    # Windows, and it ignores -o, so this has to run from the main package's own
    # directory — the only place Go will link them from.
    #
    # The `sh -c` wrapper is not decoration: before.hooks in GoReleaser OSS is a
    # list of strings, and the dir:/cmd: object form fails to parse with
    # "cannot unmarshal !!map into string".
    - >-
      sh -c "cd cmd/baobar && go run
      github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.7.0
      -platform-specific -icon ../../assets/icon/baobar.ico
      -ver-major {{ .Major }} -ver-minor {{ .Minor }} -ver-patch {{ .Patch }}
      ../../packaging/windows/versioninfo.json"
```

- [ ] **Step 8: Validate the config and run a snapshot build**

Run:
```bash
goreleaser check
goreleaser release --snapshot --clean --skip=publish
```
Expected: `check` passes; the snapshot completes and `dist/` contains Windows
archives. Then confirm the shipped exe carries the resource:

```bash
LC_ALL=C tr -d '\0' < "$(find dist -name 'baobar.exe' | head -1)" > /tmp/dist.stripped
grep -a -c BrutalSystems /tmp/dist.stripped
```
Expected: `1`. Same `LC_ALL=C` caveat as Step 4.

- [ ] **Step 9: Commit**

```bash
git add packaging/windows/versioninfo.json .goreleaser.yaml .gitignore
git commit -m "feat(windows): embed the app icon and version info in the exe"
```

---

### Task 5: Start Menu shortcut in the Chocolatey package

**Files:**
- Modify: `packaging/chocolatey/tools/chocolateyinstall.ps1`
- Modify: `packaging/chocolatey/tools/chocolateyuninstall.ps1`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Add the shortcut to the install script**

Append to `packaging/chocolatey/tools/chocolateyinstall.ps1`:

```powershell
# A tray app has no console entry point and no installer-created launcher, so
# without this there is nothing in the Start Menu and nothing to pin: the only
# way to start Baobar is to type its path.
#
# Wrapped in try/catch on purpose. This script runs under
# $ErrorActionPreference = 'Stop', and CommonPrograms (the all-users Start Menu)
# needs elevation — which Chocolatey normally has but is not guaranteed to. An
# unguarded failure here would fail the whole package *after* the binary is
# already installed and working, turning a cosmetic problem into a broken
# install. A missing shortcut is worth a warning, not an abort.
try {
  $startMenu = [Environment]::GetFolderPath('CommonPrograms')
  Install-ChocolateyShortcut `
    -ShortcutFilePath (Join-Path $startMenu 'Baobar.lnk') `
    -TargetPath (Join-Path $toolsDir 'baobar.exe') `
    -WorkingDirectory $toolsDir `
    -Description 'Menu bar indicator for OpenBao login state and token expiry'
} catch {
  Write-Warning "Baobar installed, but the Start Menu shortcut could not be created: $_"
}
```

- [ ] **Step 2: Remove it on uninstall**

Append to `packaging/chocolatey/tools/chocolateyuninstall.ps1`:

```powershell
$startMenu = [Environment]::GetFolderPath('CommonPrograms')
Remove-Item (Join-Path $startMenu 'Baobar.lnk') -ErrorAction SilentlyContinue
```

This follows the existing script's rule: remove what the package created
(`baobar.exe.gui` already sets that precedent), leave the user's config and
token alone.

- [ ] **Step 3: Parse-check both scripts**

Run:
```bash
for f in packaging/chocolatey/tools/chocolatey*.ps1; do
  pwsh -NoProfile -Command "
    \$e = \$null
    [System.Management.Automation.Language.Parser]::ParseFile('$PWD/'+'$f', [ref]\$null, [ref]\$e) > \$null
    if (\$e.Count) { \$e; exit 1 } else { Write-Host 'OK $f' }"
done
```
Expected: `OK` for both. This is a syntax check only — the Chocolatey cmdlets do
not exist on macOS, so behaviour cannot be tested here. That is a real gap, not
an oversight: record it for the Windows verification pass.

- [ ] **Step 4: Record the manual verification**

Add to the Windows section of `docs/NEXT.md`, alongside the existing Windows
checks:

```markdown
- Start Menu: after `choco install baobar`, confirm a "Baobar" entry exists in
  the Start Menu and launches the tray app; after `choco uninstall baobar`,
  confirm it is gone.
- Explorer and taskbar: confirm the exe shows the bun icon rather than the
  generic Go binary icon, and that its Properties → Details tab names
  BrutalSystems as the publisher.
```

- [ ] **Step 5: Commit**

```bash
git add packaging/chocolatey/tools/ docs/NEXT.md
git commit -m "feat(windows): add a Start Menu shortcut to the Chocolatey package"
```

---

### Task 6: Linux packages with a desktop entry and icons

**Files:**
- Create: `packaging/linux/baobar.desktop`
- Modify: `.goreleaser.yaml` (add an `nfpms` section)
- Modify: `README.md` (Linux install section, currently around lines 200-227)

**Interfaces:**
- Consumes: `assets/icon/hicolor/<n>x<n>/apps/baobar.png` from Task 3.
- Produces: `.deb` and `.rpm` artifacts.

- [ ] **Step 1: Write the applications-menu desktop entry**

Create `packaging/linux/baobar.desktop`:

```ini
[Desktop Entry]
Type=Application
Name=Baobar
GenericName=OpenBao login indicator
Comment=Shows whether you are signed in to OpenBao and how long your token has left
Exec=/usr/bin/baobar
Icon=baobar
Terminal=false
Categories=Utility;Security;
Keywords=openbao;vault;token;login;sso;
StartupNotify=false
```

This is the **applications menu** entry, installed by the package. It is not the
same thing as the autostart entry `renderDesktop` writes at runtime into
`~/.config/autostart/baobar.desktop` — that one is written by the app, points at
whatever executable is actually running, and is toggled by the tray checkbox.
Both now carry `Icon=baobar`. `StartupNotify=false` because Baobar opens no
window, so the launch spinner would never be dismissed.

- [ ] **Step 2: Add the nfpms section to `.goreleaser.yaml`**

Insert after the `archives:` block:

```yaml
# .deb/.rpm exist so that the desktop entry and the icon theme files land at
# their standard paths — a tarball cannot place them, which is why an extracted
# Baobar has no applications-menu entry and no icon. nFPM is part of GoReleaser
# OSS; it only ever packages the linux artifacts.
nfpms:
  - id: default
    ids: [crossbuilt]
    package_name: baobar
    vendor: BrutalSystems
    homepage: "https://github.com/BrutalSystems/baobar"
    maintainer: "BrutalSystems <noreply@brutalsystems.com>"
    description: "Menu bar / system tray indicator for OpenBao login state and token expiry"
    license: MIT
    formats: [deb, rpm]
    bindir: /usr/bin
    contents:
      - src: packaging/linux/baobar.desktop
        dst: /usr/share/applications/baobar.desktop
      - src: assets/icon/hicolor/512x512/apps/baobar.png
        dst: /usr/share/icons/hicolor/512x512/apps/baobar.png
      - src: assets/icon/hicolor/256x256/apps/baobar.png
        dst: /usr/share/icons/hicolor/256x256/apps/baobar.png
      - src: assets/icon/hicolor/128x128/apps/baobar.png
        dst: /usr/share/icons/hicolor/128x128/apps/baobar.png
      - src: assets/icon/hicolor/64x64/apps/baobar.png
        dst: /usr/share/icons/hicolor/64x64/apps/baobar.png
      - src: assets/icon/hicolor/48x48/apps/baobar.png
        dst: /usr/share/icons/hicolor/48x48/apps/baobar.png
      - src: assets/icon/hicolor/32x32/apps/baobar.png
        dst: /usr/share/icons/hicolor/32x32/apps/baobar.png
      - src: assets/icon/hicolor/16x16/apps/baobar.png
        dst: /usr/share/icons/hicolor/16x16/apps/baobar.png
```

The seven icon entries are written out rather than globbed: nFPM's glob handling
depends on `dst` being interpreted as a directory, and being explicit here costs
six lines and removes the question entirely.

- [ ] **Step 3: Build a snapshot and verify the package contents**

Run:
```bash
goreleaser check
goreleaser release --snapshot --clean --skip=publish
ls dist/*.deb dist/*.rpm
```
Expected: `check` passes; at least one `.deb` and one `.rpm` per architecture.

Then look inside the `.deb`. `dpkg-deb` is not installed on macOS, but a `.deb`
is an `ar` archive, and `ar` is:

```bash
cd /tmp && rm -rf debcheck && mkdir debcheck && cd debcheck
ar x "$(ls -1 ~/Source/brutalsystems/baobar/dist/*amd64.deb | head -1)"
ls                      # shows which compression data.tar uses
tar tf data.tar.*       # zst may need --zstd; gz works with tf directly
```
Expected in the listing: `./usr/bin/baobar`,
`./usr/share/applications/baobar.desktop`, and all seven
`./usr/share/icons/hicolor/<n>x<n>/apps/baobar.png` paths.

- [ ] **Step 4: Update the Linux install instructions**

> **Deviation from the spec, recorded deliberately.** The spec originally called
> for shipping the `.desktop` file and icons inside the Linux tarball as well.
> That is not done: `archives.files` in GoReleaser applies to every archive in
> the config, and the builds are split by cgo requirement (`darwin` /
> `crossbuilt`) rather than by OS, so Linux-only files cannot be added without
> also dropping a `.desktop` file into the macOS and Windows archives. A tarball
> cannot install into `/usr/share` regardless, so the packages are the honest
> answer and the README says so. The spec has been amended to match.

In `README.md`, put the package install ahead of the tarball in the Linux
section, and say plainly what the tarball does not do:

```markdown
On Debian and Ubuntu, prefer the package — it is the only install that places
the desktop entry and the icon, so Baobar appears in the applications grid:

```sh
sudo dpkg -i baobar_<version>_linux_amd64.deb
```

An `.rpm` is published for Fedora and openSUSE. The tarball below still works
and is the right choice on other distributions, but an extracted binary has no
applications-menu entry and no icon — nothing in the archive can install to
`/usr/share`.
```

Keep the existing GNOME AppIndicator warning exactly where it is; it applies to
every install method and is the difference between "invisible" and "broken".

- [ ] **Step 5: Commit**

```bash
git add packaging/linux/baobar.desktop .goreleaser.yaml README.md
git commit -m "feat(linux): ship .deb/.rpm with a desktop entry and icons"
```

---

## Open Questions

1. **The `maintainer` address in `nfpms`.** The plan uses
   `BrutalSystems <noreply@brutalsystems.com>`, which may not be a real mailbox.
   Every `.deb` and `.rpm` carries this string publicly and permanently. The git
   authorship address is a personal one and putting it in published packages is
   a decision for the owner, not a default — confirm before releasing.
