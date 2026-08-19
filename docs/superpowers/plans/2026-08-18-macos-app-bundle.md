# macOS App Bundle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship Baobar as a real `Baobar.app` in `/Applications`, signed, notarized and installable with `brew install --cask`.

**Architecture:** One shell script, `tools/macapp.sh`, owns the whole macOS
packaging chain — assemble, sign, notarize, staple, verify. GoReleaser is not
involved: every feature it would offer here is Pro-only, and keeping the chain
outside it means the same script runs on this Mac and in CI, with no CI-only code
path. CI calls the script after GoReleaser and attaches the result with
`gh release upload`. The Homebrew cask moves from generated to hand-maintained in
the tap repository.

**Tech Stack:** Apple's own tools — `codesign`, `ditto`, `xcrun notarytool`,
`xcrun stapler`, `spctl`, `plutil`. All already installed. No new dependencies of
any kind.

**Spec:** `docs/superpowers/specs/2026-08-18-desktop-integration-design.md`
(the macOS section, which this plan supersedes on two points — see Deviations).

## Verified before approval

- `Developer ID Application: Springthrough Consulting Inc. (7CQD3Q2Y8Z)` is in
  the local keychain, so Tasks 1 and 2 need no credential setup at all.
- `codesign`, `iconutil`, `hdiutil`, `ditto`, `notarytool` and `stapler` are all
  present on this machine.
- The notarization `.p8` is **not** in `~/.appstoreconnect/private_keys/` — the keys
  there belong to other projects. Task 3 therefore has to fetch it from the
  project's credential store, as its Step 2 says.
- The universal binary really is at `dist/darwin_darwin_all/baobar`, and
  `find dist -type f -name baobar -path '*darwin_all*'` matches it. Confirmed
  against a real snapshot build; `file` reports a 2-architecture Mach-O.

## Global Constraints

- **Everything must be runnable by hand on this Mac.** Any step that only works
  in CI is a design failure. CI invokes the same script with the same arguments.
- **No `.dmg`.** Homebrew casks install apps from `.zip` perfectly well. The DMG
  requirement came from GoReleaser Pro's `app:` stanza, which is not being used.
- **No GoReleaser changes for the bundle.** `app_bundles`, `dmg` and the cask
  `app:` stanza are all Pro-only. The existing `notarize:` block is removed —
  signing only the inner binary of a bundle makes macOS report it as damaged.
- Bundle identifier is `com.brutalsystems.baobar` — the same string the
  LaunchAgent already uses as its label.
- Signing identity: `Developer ID Application: Springthrough Consulting Inc.
  (7CQD3Q2Y8Z)`, confirmed present in the local keychain.
- **The cask must keep `/opt/homebrew/bin/baobar` working.** A `binary` stanza
  alongside `app` does that, so existing LaunchAgents survive the upgrade instead
  of failing at every login.

## Deviations from the spec

1. **No DMG, and the cask is hand-maintained.** The spec assumed GoReleaser could
   emit an app-based cask. It cannot without Pro (`app:` is Pro-badged and forces
   DMG output, also Pro). The cask therefore lives in `BrutalSystems/homebrew-tap`
   as hand-written Ruby, and `homebrew_casks:` is removed from `.goreleaser.yaml`.
   This costs automation: version and checksum become a release step.
2. **The bundle chain lives outside GoReleaser entirely** rather than in hooks, so
   that it is runnable by hand.

## What is testable where

| Task | Credentials needed | Testable on this Mac |
|---|---|---|
| 1. Assemble the bundle | none | fully |
| 2. Sign it | none — identity is in the keychain | fully |
| 3. Notarize and staple | Apple key from the `keep` vault | yes, after one unlock |
| 4. The cask | none | fully, from a local file |
| 5. CI wiring | repo secrets | no — by construction, it only re-runs proven steps |

---

### Task 1: Assemble the bundle

**Files:**
- Create: `tools/macapp.sh`
- Test: run by hand, verified with `plutil` and `lsappinfo`

**Interfaces:**
- Produces: `tools/macapp.sh bundle <binary> <version> [outdir]`, writing
  `<outdir>/Baobar.app`. Consumed by Tasks 2, 3 and 5.

- [x] **Step 1: Write the script's bundle subcommand**

Create `tools/macapp.sh`:

```bash
#!/usr/bin/env bash
# Package Baobar as a signed, notarized macOS .app.
#
# This deliberately does not live in .goreleaser.yaml. Every GoReleaser feature
# that would help here — app_bundles, dmg, the cask `app:` stanza, native
# notarization — is Pro-only, and hooks that only ever run inside a release are
# untestable by hand. Keeping the chain in one script means the command that
# runs on a laptop is the command that runs in CI.
#
#   ./tools/macapp.sh bundle   dist/baobar 0.1.6 dist
#   ./tools/macapp.sh sign     dist/Baobar.app
#   ./tools/macapp.sh notarize dist/Baobar.app 0.1.6 dist
#   ./tools/macapp.sh verify   dist/Baobar.app
set -euo pipefail

IDENTITY="${MACOS_SIGN_IDENTITY:-Developer ID Application: Springthrough Consulting Inc. (7CQD3Q2Y8Z)}"
BUNDLE_ID="com.brutalsystems.baobar"

die() { printf 'macapp: %s\n' "$*" >&2; exit 1; }

# CFBundleShortVersionString must be dotted digits. Snapshot versions like
# 0.1.6-SNAPSHOT-abc1234 are not, and a malformed value makes the bundle look
# corrupt rather than failing loudly, so it is trimmed at the first dash.
numeric_version() { printf '%s' "${1#v}" | cut -d- -f1; }

cmd_bundle() {
  local binary="${1:?usage: bundle <binary> <version> [outdir]}"
  local version="${2:?usage: bundle <binary> <version> [outdir]}"
  local outdir="${3:-dist}"
  local app="$outdir/Baobar.app"
  local short; short="$(numeric_version "$version")"

  [ -f "$binary" ] || die "no such binary: $binary"
  [ -f assets/icon/baobar.icns ] || die "assets/icon/baobar.icns is missing; run: go run ./tools/genappicon"

  rm -rf "$app"
  mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
  cp "$binary" "$app/Contents/MacOS/baobar"
  chmod +x "$app/Contents/MacOS/baobar"
  cp assets/icon/baobar.icns "$app/Contents/Resources/baobar.icns"

  # LSUIElement keeps Baobar out of the Dock and the app switcher. The process
  # already runs as BackgroundOnly without it — systray sets that at runtime —
  # but declaring it makes the behaviour a property of the app rather than an
  # accident of initialisation order.
  #
  # LSMinimumSystemVersion is deliberately absent: stating a floor we have not
  # tested is worse than stating none, and Go's own minimum already applies.
  cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleName</key><string>Baobar</string>
	<key>CFBundleDisplayName</key><string>Baobar</string>
	<key>CFBundleIdentifier</key><string>$BUNDLE_ID</string>
	<key>CFBundleExecutable</key><string>baobar</string>
	<key>CFBundleIconFile</key><string>baobar</string>
	<key>CFBundleShortVersionString</key><string>$short</string>
	<key>CFBundleVersion</key><string>$short</string>
	<key>LSUIElement</key><true/>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

  plutil -lint "$app/Contents/Info.plist" >/dev/null || die "generated Info.plist is malformed"
  printf 'built %s (version %s)\n' "$app" "$short"
}

case "${1:-}" in
  bundle) shift; cmd_bundle "$@" ;;
  *) die "usage: macapp.sh bundle <binary> <version> [outdir]" ;;
esac
```

- [x] **Step 2: Build a binary and bundle it**

Run:
```bash
chmod +x tools/macapp.sh
go build -o dist/baobar-local ./cmd/baobar
./tools/macapp.sh bundle dist/baobar-local 0.1.6 dist
find dist/Baobar.app -type f | sort
```
Expected: exactly three files — `Contents/Info.plist`, `Contents/MacOS/baobar`,
`Contents/Resources/baobar.icns`.

- [x] **Step 3: Verify macOS actually reads the bundle**

Run:
```bash
plutil -p dist/Baobar.app/Contents/Info.plist | grep -E 'Identifier|Executable|UIElement|ShortVersion'
mdls -name kMDItemDisplayName -name kMDItemVersion dist/Baobar.app 2>/dev/null
```
Expected: the identifier is `com.brutalsystems.baobar`, `LSUIElement` is 1, and
the version is `0.1.6` with no snapshot suffix.

- [x] **Step 4: Launch it and confirm the identity macOS sees**

Run:
```bash
open dist/Baobar.app
sleep 3
lsappinfo info -only name,bundleid $(pgrep -f 'Baobar.app/Contents/MacOS/baobar')
```
Expected: `CFBundleIdentifier` is `com.brutalsystems.baobar` and the display name
is `Baobar` — where an unbundled binary reports `bundleID=[ NULL ]` and the
lowercase name `baobar`. That difference is the entire point of this task.

Also look at the menu bar: the tray icon must still appear. Then quit it from the
tray menu, or `pkill -f 'Baobar.app/Contents/MacOS/baobar'`.

- [x] **Step 5: Look at the icon in Finder**

Run: `open dist/`

Expected: `Baobar.app` shows the cream bun icon rather than a generic application
icon. If it shows the generic icon, `CFBundleIconFile` and the `.icns` filename
disagree, or the `.icns` is malformed — check with
`file assets/icon/baobar.icns`.

- [x] **Step 6: Commit**

```bash
git add tools/macapp.sh
git commit -m "feat(macos): assemble a Baobar.app bundle"
```

---

### Task 2: Sign the bundle

**Files:**
- Modify: `tools/macapp.sh` (add `sign` and `verify`)

**Interfaces:**
- Consumes: `cmd_bundle` output from Task 1.
- Produces: `tools/macapp.sh sign <app>` and `tools/macapp.sh verify <app>`.

- [x] **Step 1: Confirm the identity is present before writing anything**

Run: `security find-identity -v -p codesigning | grep "Developer ID Application"`
Expected: one line naming Springthrough Consulting Inc. (7CQD3Q2Y8Z). If it is
absent, stop — the rest of this task cannot be tested and must not be guessed at.

- [x] **Step 2: Add the sign and verify subcommands**

Insert into `tools/macapp.sh`, before the `case` block:

```bash
cmd_sign() {
  local app="${1:?usage: sign <app>}"
  [ -d "$app" ] || die "no such bundle: $app"

  # --options runtime enables the hardened runtime, which notarization requires.
  # --timestamp fetches a trusted timestamp, without which the signature expires
  # with the certificate. No entitlements file: a Go binary that only opens
  # sockets and reads its own config needs no exceptions, and every entitlement
  # added is another thing Apple can reject.
  #
  # Not --deep, which Apple explicitly discourages: it signs nested code with the
  # wrong identity settings and hides errors. This bundle has one executable.
  codesign --force --options runtime --timestamp --sign "$IDENTITY" "$app"
  printf 'signed %s\n' "$app"
}

cmd_verify() {
  local app="${1:?usage: verify <app>}"
  codesign --verify --strict --verbose=2 "$app"
  codesign -dv --verbose=2 "$app" 2>&1 | grep -E 'Authority|TeamIdentifier|flags'
  # Gatekeeper's own answer. Before notarization this reports rejected; after
  # stapling it must say "accepted, source=Notarized Developer ID".
  spctl -a -vv -t exec "$app" || true
}
```

And extend the dispatcher:

```bash
case "${1:-}" in
  bundle) shift; cmd_bundle "$@" ;;
  sign) shift; cmd_sign "$@" ;;
  verify) shift; cmd_verify "$@" ;;
  *) die "usage: macapp.sh {bundle|sign|verify} ..." ;;
esac
```

- [x] **Step 3: Sign and inspect**

Run:
```bash
./tools/macapp.sh sign dist/Baobar.app
./tools/macapp.sh verify dist/Baobar.app
```
Expected: `--verify --strict` prints `valid on disk` and `satisfies its
Designated Requirement`. The authority line names
`Developer ID Application: Springthrough Consulting Inc.`, `TeamIdentifier` is
`7CQD3Q2Y8Z`, and `flags` includes `runtime` — that last one is the hardened
runtime, and notarization fails without it.

- [x] **Step 4: Confirm Gatekeeper still refuses it**

In the same output, `spctl` is expected to **reject** the app at this point,
typically `source=Unnotarized Developer ID`. That is correct and important: it
proves signing alone is not enough, and gives Task 3 a real before/after.

- [x] **Step 5: Commit**

```bash
git add tools/macapp.sh
git commit -m "feat(macos): sign the bundle with the hardened runtime"
```

---

### Task 3: Notarize and staple

The one task needing a credential. The App Store Connect `.p8` is not on disk —
it lives in the project's encrypted credential store, which decrypts through
OpenBao and therefore needs a live token.

**Files:**
- Modify: `tools/macapp.sh` (add `notarize`)

**Interfaces:**
- Consumes: a signed bundle from Task 2.
- Produces: `tools/macapp.sh notarize <app> <version> [outdir]`, writing
  `<outdir>/Baobar-<version>-macOS.zip` — stapled, and the artifact the cask
  downloads.

- [x] **Step 1: Add the notarize subcommand**

Insert into `tools/macapp.sh`, before the `case` block, and add
`notarize) shift; cmd_notarize "$@" ;;` to the dispatcher:

```bash
cmd_notarize() {
  local app="${1:?usage: notarize <app> <version> [outdir]}"
  local version="${2:?usage: notarize <app> <version> [outdir]}"
  local outdir="${3:-dist}"
  local zip="$outdir/Baobar-$(numeric_version "$version")-macOS.zip"

  : "${APPLE_NOTARY_KEY_FILE:?set APPLE_NOTARY_KEY_FILE to the AuthKey .p8 path}"
  : "${APPLE_NOTARY_KEY_ID:?set APPLE_NOTARY_KEY_ID}"
  : "${APPLE_NOTARY_ISSUER_ID:?set APPLE_NOTARY_ISSUER_ID}"

  # notarytool takes an archive, not a bundle. ditto is the required tool here:
  # `zip` does not preserve the symlinks and extended attributes inside a
  # bundle, and the result fails notarization for reasons that read as unrelated.
  local submit="$outdir/notarize-submission.zip"
  ditto -c -k --keepParent "$app" "$submit"

  xcrun notarytool submit "$submit" \
    --key "$APPLE_NOTARY_KEY_FILE" \
    --key-id "$APPLE_NOTARY_KEY_ID" \
    --issuer "$APPLE_NOTARY_ISSUER_ID" \
    --wait
  rm -f "$submit"

  # The ticket staples to the bundle, not to the archive — so the app must be
  # stapled first and re-zipped afterwards. Zipping before stapling produces an
  # archive that passes notarization and still warns the user on first launch
  # if they are offline. This ordering is the whole reason the step exists.
  xcrun stapler staple "$app"
  xcrun stapler validate "$app"

  rm -f "$zip"
  ditto -c -k --keepParent "$app" "$zip"
  printf 'notarized and stapled: %s\n' "$zip"
  shasum -a 256 "$zip"
}
```

- [x] **Step 2: Unlock the credential**

Fetch the App Store Connect key from the project's credential store, decode it to
a temporary file, and point the three environment variables at it:

```bash
# ... retrieve the base64-encoded .p8 from your credential store, then:
printf %s "$ASC_KEY_P8_B64" | base64 -d > "$TMPDIR/AuthKey.p8"
export APPLE_NOTARY_KEY_FILE="$TMPDIR/AuthKey.p8"
export APPLE_NOTARY_KEY_ID=<the App Store Connect key ID>
export APPLE_NOTARY_ISSUER_ID=<the issuer UUID>
```

The `.p8` is stored base64-encoded because a dotenv-style store cannot hold a
multi-line PEM. Delete the decoded file when finished — it is a signing key.

- [x] **Step 3: Notarize**

Run: `./tools/macapp.sh notarize dist/Baobar.app 0.1.6 dist`

Expected: `notarytool` prints `status: Accepted`, then `stapler validate` prints
`The validate action worked!`, then a zip path and its SHA-256.

If it comes back `Invalid`, get the reason rather than guessing:
```bash
xcrun notarytool log <submission-id> --key "$APPLE_NOTARY_KEY_FILE" \
  --key-id "$APPLE_NOTARY_KEY_ID" --issuer "$APPLE_NOTARY_ISSUER_ID"
```
The usual causes are a missing hardened runtime and an unsigned nested binary.

- [x] **Step 4: Prove Gatekeeper's answer changed**

Run: `./tools/macapp.sh verify dist/Baobar.app`

Expected: `spctl` now reports **`accepted`** with
`source=Notarized Developer ID`, where Task 2 Step 4 saw a rejection. That
before/after is the evidence this whole plan exists to produce.

Then test it the way a user meets it — a quarantined copy, which is what a
downloaded file is:
```bash
cp -R dist/Baobar.app /tmp/Baobar.app
xattr -w com.apple.quarantine "0081;00000000;Safari;" /tmp/Baobar.app
spctl -a -vv -t exec /tmp/Baobar.app
open /tmp/Baobar.app   # must launch with no Gatekeeper dialog at all
pkill -f '/tmp/Baobar.app' ; rm -rf /tmp/Baobar.app
```

- [x] **Step 5: Commit**

```bash
git add tools/macapp.sh
git commit -m "feat(macos): notarize and staple the bundle"
```

---

### Task 4: The Homebrew cask

**Files:**
- Modify: `.goreleaser.yaml` (remove `homebrew_casks:`)
- Create: `packaging/homebrew/baobar.rb` (the cask, copied to the tap on release)

**Interfaces:**
- Consumes: the zip and SHA-256 from Task 3.
- Produces: a cask installing `Baobar.app` while keeping
  `/opt/homebrew/bin/baobar` valid.

- [x] **Step 1: Write the cask**

Create `packaging/homebrew/baobar.rb`:

```ruby
cask "baobar" do
  version "0.1.6"
  sha256 "REPLACE_ON_RELEASE"

  url "https://github.com/BrutalSystems/baobar/releases/download/v#{version}/Baobar-#{version}-macOS.zip"
  name "Baobar"
  desc "Menu bar indicator for OpenBao login state and token expiry"
  homepage "https://github.com/BrutalSystems/baobar"

  depends_on macos: ">= :big_sur"

  app "Baobar.app"

  # Keeps /opt/homebrew/bin/baobar resolving after the move into /Applications.
  # Without it, every LaunchAgent written by an older version points at a path
  # that no longer exists, and launchd fails the spawn at each login while the
  # tray checkbox reports unchecked — Enabled() only stats the target, so
  # nothing in the UI reveals it.
  binary "#{appdir}/Baobar.app/Contents/MacOS/baobar", target: "baobar"

  uninstall quit:       "com.brutalsystems.baobar",
            launchctl:  "com.brutalsystems.baobar",
            login_item: "Baobar"

  # ~/.vault-token is deliberately absent: Baobar reads and writes it, but it
  # belongs to the `bao` CLI and SOPS. Zapping Baobar must not log you out of
  # your terminal.
  zap trash: [
    "~/Library/LaunchAgents/com.brutalsystems.baobar.plist",
    "~/Library/Application Support/baobar",
    "~/Library/Caches/baobar",
  ]
end
```

- [x] **Step 2: Remove GoReleaser's cask generation**

Delete the entire `homebrew_casks:` block from `.goreleaser.yaml`. Leaving it
would overwrite this hand-written cask in the tap on the next release.

Run: `goreleaser check`
Expected: passes.

- [x] **Step 3: Test the cask against the local zip**

Homebrew **refuses casks that are not in a tap** ("Homebrew requires casks to be
in a tap, rejecting: ..."), so a bare file path does not work. Use a scratch tap.

Test with an *isolated* copy, not the real cask: installing a second cask named
`baobar` collides with the installed one, and running the real uninstall would
execute `launchctl: com.brutalsystems.baobar` and tear down your own working
LaunchAgent. A distinct token, binary target and launchctl label exercise the
same code paths harmlessly.

```bash
SHA=$(shasum -a 256 dist/Baobar-0.1.6-macOS.zip | cut -d' ' -f1)
sed -e 's|^cask "baobar" do|cask "baobar-casktest" do|' \
    -e "s|REPLACE_ON_RELEASE|$SHA|" \
    -e "s|url \".*\"|url \"file://$PWD/dist/Baobar-0.1.6-macOS.zip\"|" \
    -e 's|target: "baobar"|target: "baobar-casktest"|' \
    -e 's|com.brutalsystems.baobar"|com.brutalsystems.baobar-casktest"|g' \
    packaging/homebrew/baobar.rb > /tmp/baobar-casktest.rb

brew tap-new baobar/casktest --no-git
cp /tmp/baobar-casktest.rb "$(brew --repository baobar/casktest)/Casks/"
brew install --cask baobar/casktest/baobar-casktest
```
Expected: `Moving App 'Baobar.app' to '/Applications/Baobar.app'` and
`Linking Binary ... to '/opt/homebrew/bin/baobar-casktest'`.

Clean up afterwards with `brew uninstall --cask baobar-casktest` and
`brew untap baobar/casktest`.

Then verify both halves of the install:
```bash
ls -d /Applications/Baobar.app
ls -l "$(brew --prefix)/bin/baobar"
```
Expected: the app is in `/Applications`, and the `binary` stanza has left a
`baobar` symlink in Homebrew's bin — the compatibility that keeps existing
LaunchAgents alive.

- [x] **Step 4: Test the uninstall teardown**

```bash
brew uninstall --cask baobar
ls -d /Applications/Baobar.app 2>&1    # expected: No such file
```
Expected: the app is gone. If you had a LaunchAgent loaded, confirm
`launchctl list | grep baobar` is now empty — that is the `uninstall launchctl:`
stanza doing the job that had to be done by hand at the start of this work.

- [x] **Step 5: Commit**

```bash
git add packaging/homebrew/baobar.rb .goreleaser.yaml
git commit -m "feat(macos): hand-maintained cask installing Baobar.app"
```

---

### Task 5: Wire it into the release

Deliberately last, and deliberately thin: by this point every command below has
already been run by hand and proven on this Mac. CI only repeats them.

**Files:**
- Modify: `.github/workflows/release.yml`
- Modify: `.goreleaser.yaml` (remove `notarize:`)
- Modify: `README.md`

- [x] **Step 1: Remove the old notarization block**

Delete the `notarize:` block from `.goreleaser.yaml`. It signs the bare binary
via the cross-platform path, which must not be applied to a bundle — GoReleaser's
own documentation states that signing only the binary inside a bundle "will cause
macOS to mark the application as damaged". The bundle is signed by
`tools/macapp.sh` instead.

Run: `goreleaser check`
Expected: passes.

- [x] **Step 2: Add the packaging step to the release workflow**

In `.github/workflows/release.yml`, after the GoReleaser step:

```yaml
      # The macOS bundle is packaged outside GoReleaser: every feature that
      # would do this for us is Pro-only. tools/macapp.sh is the same script
      # that is run by hand on a laptop, which is why this step is three lines
      # and not a pipeline of its own.
      - name: Package, sign and notarize Baobar.app
        env:
          MACOS_SIGN_P12: ${{ secrets.MACOS_SIGN_P12 }}
          MACOS_SIGN_PASSWORD: ${{ secrets.MACOS_SIGN_PASSWORD }}
          KEYCHAIN_PASSWORD: ${{ secrets.KEYCHAIN_PASSWORD }}
          APPLE_NOTARY_KEY_B64: ${{ secrets.APPLE_NOTARY_KEY_B64 }}
          APPLE_NOTARY_KEY_ID: ${{ secrets.APPLE_NOTARY_KEY_ID }}
          APPLE_NOTARY_ISSUER_ID: ${{ secrets.APPLE_NOTARY_ISSUER_ID }}
        run: |
          set -euo pipefail
          KEYCHAIN="$RUNNER_TEMP/signing.keychain-db"
          security create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
          security set-keychain-settings -lut 21600 "$KEYCHAIN"
          security unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
          echo -n "$MACOS_SIGN_P12" | base64 --decode -o "$RUNNER_TEMP/sign.p12"
          security import "$RUNNER_TEMP/sign.p12" -P "$MACOS_SIGN_PASSWORD" -A -t cert -f pkcs12 -k "$KEYCHAIN"
          security set-key-partition-list -S apple-tool:,apple: -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
          security list-keychain -d user -s "$KEYCHAIN"

          echo -n "$APPLE_NOTARY_KEY_B64" | base64 --decode -o "$RUNNER_TEMP/AuthKey.p8"
          export APPLE_NOTARY_KEY_FILE="$RUNNER_TEMP/AuthKey.p8"

          VERSION="${GITHUB_REF_NAME#v}"
          BINARY="$(find dist -type f -name baobar -path '*darwin_all*' | head -1)"
          test -n "$BINARY" || { echo "no universal darwin binary in dist/"; exit 1; }

          ./tools/macapp.sh bundle "$BINARY" "$VERSION" dist
          ./tools/macapp.sh sign dist/Baobar.app
          ./tools/macapp.sh notarize dist/Baobar.app "$VERSION" dist
          ./tools/macapp.sh verify dist/Baobar.app

      - name: Attach Baobar.app to the release
        run: gh release upload "$GITHUB_REF_NAME" dist/Baobar-*-macOS.zip
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

The notarization key secret is the base64 of the App Store Connect `.p8`, which is
exactly how the credential store already holds it — copy the value across without
decoding.

- [x] **Step 3: Document the cask release step**

Add to `docs/NEXT.md`, under a new "Releasing" heading:

```markdown
### Releasing macOS

The Homebrew cask is hand-maintained; GoReleaser no longer generates it, because
an app-based cask needs GoReleaser Pro. After a release publishes:

1. Take the SHA-256 that `tools/macapp.sh notarize` printed (the CI log has it).
2. Update `version` and `sha256` in `packaging/homebrew/baobar.rb`.
3. Copy it to `BrutalSystems/homebrew-tap` as `Casks/baobar.rb` and push.

This is the automation the Pro licence would have bought. Two lines per release.
```

- [x] **Step 4: Update the README install section**

The macOS install becomes an app rather than a binary. Change the Homebrew
section to say Baobar installs to `/Applications`, appears in Spotlight and
Launchpad, and that `baobar` remains on `PATH` for compatibility. Keep the
"Why the signature says Springthrough" section unchanged — it is still accurate
and still the right thing to explain.

Also update the verification command, which no longer points at a bare binary:

```bash
codesign --test-requirement="=notarized" -vv /Applications/Baobar.app
```

- [x] **Step 5: Commit**

```bash
git add .github/workflows/release.yml .goreleaser.yaml README.md docs/NEXT.md
git commit -m "ci(macos): build, sign and notarize the app bundle on release"
```

---

## Risks

| Risk | Mitigation |
|---|---|
| A broken signature reaches users as "Baobar is damaged" | Tasks 1-4 all run on this Mac before CI is touched at all. Task 3 Step 4 tests a genuinely quarantined copy, which is the state a downloaded app is actually in. |
| Stapling before zipping, producing an archive that warns offline users | The ordering is enforced inside `cmd_notarize` and asserted by `stapler validate`, rather than left to whoever edits the workflow. |
| Existing LaunchAgents break when the install path moves | The cask's `binary` stanza keeps `/opt/homebrew/bin/baobar` resolving. |
| The hand-maintained cask drifts from the released version | It is two fields, and Task 5 Step 3 writes the procedure down. Accepted cost of not buying Pro. |
| Notarization credentials expire or rotate | The key lives in the Apple `keep` vault; `bao login` is a prerequisite, and Baobar itself is the thing that tells you when that token is about to expire. |

## Open Question — resolved

`depends_on macos:` was dropped rather than guessed at. An absent constraint is
better than a wrong one, and nothing observed during implementation justified a
specific floor.

## Outcome

Tasks 1-4 were executed and verified on a Mac before Task 5 was written, which
was the point of the structure. What that caught, that CI would not have:

- **App Translocation.** Launching a genuinely quarantined copy showed macOS
  running it from a randomised `AppTranslocation` path. `checkStablePath` already
  refuses it — `/private/var/folders/` was in `volatilePrefixes` for `/tmp`'s
  sake — but its advice named `/usr/local/bin` to someone who had double-clicked
  an app. Now pinned by a test using the real observed path.
- **Homebrew refuses casks outside a tap.** The plan's original local-test step
  could not have worked.
- **Testing the real cask would have torn down a live LaunchAgent**, since its
  uninstall stanza names the real launchctl label.

Secrets needed: none new. The existing `MACOS_SIGN_*` and `MACOS_NOTARY_*`
repository secrets cover it.
