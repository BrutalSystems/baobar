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

case "${1:-}" in
  bundle) shift; cmd_bundle "$@" ;;
  sign) shift; cmd_sign "$@" ;;
  notarize) shift; cmd_notarize "$@" ;;
  verify) shift; cmd_verify "$@" ;;
  *) die "usage: macapp.sh {bundle|sign|verify} ..." ;;
esac
