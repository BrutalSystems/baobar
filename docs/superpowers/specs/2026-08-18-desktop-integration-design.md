# Desktop integration: app icon and launcher entries

Baobar ships as a bare binary on all three platforms. It has no application
icon, and on macOS no way to launch it except a terminal or launchd. This adds
a real icon and the platform-native launcher entry that goes with it.

## Goal

One icon asset, three platform treatments:

| Platform | What lands | Where |
|---|---|---|
| Windows | `.ico` + `VERSIONINFO` embedded in the exe; Start Menu shortcut | exe resources; `%ProgramData%\...\Start Menu\Programs\Baobar.lnk` |
| Linux | `.desktop` entry + hicolor PNGs, via `.deb`/`.rpm` | `/usr/share/applications`, `/usr/share/icons/hicolor/*/apps` |
| macOS | `Baobar.app` bundle with `.icns` | `/Applications` |

## Non-goals

- **Redesigning the tray state icons.** The five 22px state glyphs
  (`internal/tray/assets`) stay as they are. They are a different asset with a
  different job — state signalling, with the silhouette test that enforces it —
  and they are not derived from the app icon. `docs/NEXT.md` item 4 proposes
  rendering the bun as a macOS template image for the menu bar; that is separate
  work and is not in scope here.
- **Signing Windows or Linux binaries.** Still unsigned; issue #11.
- **Fixing notification attribution.** `beeep` shells out to `osascript` on
  macOS, so expiry notifications are attributed to Script Editor rather than to
  Baobar. A bundle identifier is a prerequisite for fixing that, but the fix
  itself needs a different notification path and is out of scope.

## Current state

- `.goreleaser.yaml` builds darwin natively (cgo, required by systray's
  Objective-C backend) and cross-builds linux/windows with `CGO_ENABLED=0`.
  Everything runs on a macOS runner.
- macOS is signed and notarized through GoReleaser's **cross-platform** notarize
  path (quill), gated on `MACOS_SIGN_P12` being set.
- The Homebrew cask installs a **binary**, not an app: `/opt/homebrew/bin/baobar`.
- Verified on the running process: no bundle ID, no version, and
  `type="BackgroundOnly"` — so there is no spurious Dock icon today. The gap is
  launchability and identity, not Dock behaviour.
- The Windows exe has **no icon resource and no VERSIONINFO** at all: a generic
  icon in Explorer, the taskbar, and the SmartScreen dialog, with no publisher
  string.
- `renderDesktop` in `internal/autostart/render.go` writes the XDG autostart
  entry with no `Icon=` key, so even today's autostart entry is iconless.

## Icon asset pipeline

Source artwork is `assets/icon/baobar.svg` (supplied; a bao bun, `#050f1e` mark
with `#fefefe` pleat lines, 4 flat paths, no gradients or masks).

**The SVG is not rasterized by the build.** No pure-Go SVG rasterizer is a
dependency, and the file expresses its fills as a CSS `<style>` block that such
rasterizers typically ignore. Instead a pre-rendered master PNG is committed
alongside the SVG and is the input to everything:

    assets/icon/baobar.svg          design source of truth, hand-edited
    assets/icon/baobar-1024.png     rendered master, committed, build input
    assets/icon/baobar-32.png       committed override, tighter inset
    assets/icon/baobar-16.png       committed override, tighter inset
    tools/mkmaster.py               how those three were produced

`tools/genappicon` reads the master and writes every derived asset:

    assets/icon/baobar.icns              icns.Encode  (one call, builds the set)
    assets/icon/baobar.ico               ico.EncodeAll (256/128/64/48/32/16)
    assets/icon/hicolor/<n>x<n>/baobar.png  (512/256/128/64/48/32/16)

This needs **no new dependencies**. `github.com/jackmordaunt/icns/v3`,
`github.com/sergeymakinen/go-ico` and `github.com/nfnt/resize` are already in
`go.sum` as indirect dependencies of `beeep`, and already recorded in
`THIRD_PARTY_LICENSES.md`. They get promoted to direct requires; no new licence
entries, no new supply chain.

Generated assets are committed rather than built in CI, matching how
`tools/genicons` already works: run the generator, commit the output, and the
release pipeline only consumes files.

### The master PNG must carry its own ground

The mark is near-black (`#050f1e`). Placed bare it vanishes on a dark Dock, a
dark Windows taskbar, and dark-mode Launchpad. macOS also does not round app
icons for you the way iOS does — a full-bleed square asset renders as a square
tile next to every squircle in Launchpad.

The master therefore has the ground composed into it:

- 1024x1024, sRGB, PNG
- rounded-rect ground filled with cream `#F2E3C6`, 18% corner radius
- mark spanning 80% of the tile width, centred
- **transparent outside the rounded rect**

One asset satisfies all three platforms: correct on macOS, a rounded tile on the
Windows taskbar, fine on Linux.

Cream rather than the tray's green `#3ba55d`: in this application green already
means *signed in*, and a permanently green app icon spends a colour the tray
needs to carry state. The ground is also what keeps the near-black mark visible
on a dark dock, and what gives the tile a visible edge against a white Finder
window — which a warm white ground does not.

`tools/mkmaster.py` records how the PNGs are produced from the SVG. It is
one-time asset tooling, not part of the build: it needs Chrome (which honours the
CSS `<style>` fills that pure-Go rasterisers ignore) and Pillow, neither of which
is a project dependency. That is precisely why its output is committed.

### Small sizes

Two problems appear below about 48px, and they are different problems.

The white pleat lines blur into the bun under Lanczos downscaling. That is
accepted: the silhouette still reads clearly as a bun, which is what an icon that
small has to do.

The inset does not survive, and that is fixed. A mark spanning 80% of the tile
reads well at 1024 and wastes pixels at 16, where the bun ends up small inside its
own tile with ground it cannot afford. `tools/mkmaster.py` therefore composes 16
and 32 separately, with the mark spanning 92% and 88%, and `genappicon` prefers a
committed `assets/icon/baobar-<n>.png` over a downscale whenever one exists. An
override whose dimensions do not match its filename is ignored rather than
trusted — a mis-sized file is a mistake, and honouring it would produce a
malformed `.ico` that fails much later.

The `.icns` is the exception: `icns.Encode` derives its own set from the single
image it is given, so macOS small sizes still come from the master. Acceptable,
because macOS never renders the app icon as small as Explorer's 16px list view.

## Windows

1. **Embed the icon and version info** with `goversioninfo` v1.7.0, invoked as
   `go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.7.0`
   from a `before.hooks` step. Run that way it never enters `go.mod` and adds no
   direct dependency; it is a build tool, not a library.
   - Its `-platform-specific` flag writes `resource_windows_amd64.syso` and
     `resource_windows_arm64.syso` in one invocation. Go links `.syso` files in
     the `main` package automatically, and those filename suffixes are what keep
     them out of the darwin and linux builds.
   - Config lives in `packaging/windows/versioninfo.json`
     (`FileDescription`, `CompanyName`, `ProductName`, `LegalCopyright`), with
     the version passed on the command line from the build.
   - Output goes into `cmd/baobar/` and is gitignored, so a binary artifact does
     not live in the tree.
   - This is the only generated asset that must be rebuilt per release rather
     than per artwork change, because the version string is baked in.

   Hand-writing the `.syso` was considered and rejected: it means emitting a PE
   COFF object with a `.rsrc` section, which is a serious piece of work to own
   for no gain over a mature tool that never touches the dependency graph.
2. **Start Menu shortcut** in `chocolateyinstall.ps1` via
   `Install-ChocolateyShortcut`, pointing at the extracted `baobar.exe`.
   `chocolateyuninstall.ps1` removes it — the existing script already sets the
   precedent of cleaning up what it created (`baobar.exe.gui`) while leaving
   user config alone.
3. The winget manifest needs no change.

## Linux

1. **`nfpms` in `.goreleaser.yaml`** (free in GoReleaser OSS) producing `.deb`
   and `.rpm` that install the binary to `/usr/bin/baobar`, a `.desktop` entry
   to `/usr/share/applications/baobar.desktop`, and the hicolor PNGs under
   `/usr/share/icons/hicolor/<n>x<n>/apps/baobar.png`.
2. **The tarball does not carry them.** GoReleaser's `archives.files` applies to
   every archive in its config, and the builds are split by cgo requirement
   (`darwin` / `crossbuilt`) rather than by OS — so there is no clean way to add
   Linux-only files without also dropping a `.desktop` file into the macOS and
   Windows archives. Since a tarball cannot install to `/usr/share` anyway, the
   README instead points Debian and Ubuntu users at the package and states
   plainly that an extracted binary has no menu entry and no icon.
3. **Add `Icon=baobar` to `renderDesktop`.** A theme name, not a path, per the
   Desktop Entry spec. It resolves for package installs; for tarball users it
   simply does not resolve, which is what happens today anyway — no regression.
   `desktopTarget` parses only the `Exec=` line, so the round-trip test in
   `autostart_test.go` is unaffected, but a test should assert the key is
   present so it cannot silently disappear.

## macOS

The expensive one, and the one that replaces something that currently works.

1. **Assemble the bundle in a hook.** GoReleaser's `app_bundles` and `dmg`
   stanzas are Pro-only ("This feature is exclusively available with GoReleaser
   Pro") and this project is on OSS. So `tools/mkbundle.sh` builds:

        Baobar.app/Contents/Info.plist
        Baobar.app/Contents/MacOS/baobar
        Baobar.app/Contents/Resources/baobar.icns

   `Info.plist` sets `CFBundleIdentifier = com.brutalsystems.baobar` — the same
   string the LaunchAgent already uses as its label — plus `CFBundleName Baobar`,
   `CFBundleExecutable baobar`, `CFBundleIconFile`, `CFBundleShortVersionString`,
   `LSUIElement true`, `NSHighResolutionCapable true`, `CFBundlePackageType APPL`.
   `LSUIElement` is belt-and-braces: the process is already `BackgroundOnly`
   without it, but declaring it makes that a property of the app rather than an
   accident of how systray initialises.

2. **Switch to native signing.** This is the substantive risk. The
   cross-platform notarize path in use today must not be applied to a bundle —
   GoReleaser's docs state that signing or notarizing only the binary inside a
   bundle "will cause macOS to mark the application as damaged." So:

   - CI creates a temporary keychain, imports the `.p12`, and stores a
     `notarytool` credential profile (the workflow recipe is in GoReleaser's
     notarize docs).
   - `codesign --force --options runtime --timestamp --sign "<Developer ID>"` on
     the bundle. Hardened runtime is required for notarization. Not `--deep`,
     which Apple discourages.
   - `ditto -c -k --keepParent` the bundle, submit the zip to `notarytool`,
     then `xcrun stapler staple Baobar.app` and re-zip. The ticket staples to
     the bundle, not to a zip, so the order matters.
   - The existing `notarize:` block is removed, not kept alongside.

   The Apple credential for this already exists in the project's credential store
   and as a repository secret; no new account setup is needed.

3. **Cask ships both stanzas.** An `app` stanza for `Baobar.app` and a `binary`
   stanza symlinking the inner executable, so `/opt/homebrew/bin/baobar` keeps
   working. This is what keeps existing LaunchAgents valid: a plist pointing at
   the old path continues to resolve, so nobody's "Start at login" silently
   stops working on upgrade.

   Without the binary stanza the install path moves and every existing
   LaunchAgent breaks the same way the stale `/tmp/baobar` one did — launchd
   fails at each login while `Enabled()` reports unchecked, because it only
   stats the target. Shipping both avoids needing a migration at all.

## Risks

| Risk | Mitigation |
|---|---|
| Notarization change ships a "Baobar is damaged" build | Build, sign, notarize and staple locally first; verify with `codesign --test-requirement="=notarized" -vv` and `spctl -a -vv` before touching CI. Do not merge on CI green alone. |
| `.syso` breaks non-Windows builds | Filename suffixes constrain it to windows/$arch. `GOOS=linux go build` and the darwin build in CI both prove it. |
| 16px icon illegible | Hand-export override, per Small sizes above. |
| Existing LaunchAgents break on cask upgrade | Ship the `binary` stanza alongside `app`. |

## Testing

- `genappicon` output: assert every declared size exists, is square, has the
  expected dimensions, and is non-empty; assert the `.ico` decodes via
  `ico.DecodeAll` to the expected size set.
- `renderDesktop`: assert `Icon=baobar` present; existing `desktopTarget`
  round-trip test must still pass.
- Windows: on real hardware, confirm the exe shows the icon in Explorer and the
  taskbar, that Get Info-equivalent shows the publisher string, and that the
  Start Menu shortcut launches it. `docs/NEXT.md` already documents a Windows
  verification pass; extend it.
- Linux: `dpkg -i` the `.deb` on Ubuntu 24.04, confirm Baobar appears in the
  applications grid with the icon, and that the autostart entry shows an icon in
  the startup-apps UI.
- macOS: local notarization verification as above, then confirm the app appears
  in `/Applications`, launches from Spotlight, shows "Baobar" rather than
  "baobar", and that `lsappinfo` reports the bundle ID and version.

## Order of work

Windows, then Linux, then macOS. Inverse of the apparent priority, deliberately:
Windows is both the cheapest fix and the most visibly broken, and Windows plus
Linux exercise the entire icon generation pipeline with no signing risk. By the
time macOS is touched, the only new variable is notarization.

## Open question

The master PNG has to come from Illustrator, per Icon asset pipeline above —
1024x1024, ground composed in, transparent corners. Everything else is blocked
on that one file.
