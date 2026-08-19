cask "baobar" do
  version "0.1.7"
  sha256 "08dd415a5bb67520f4e265f7962d38eafccfa15334fae9468b7d0ba4b87e508b"

  url "https://github.com/BrutalSystems/baobar/releases/download/v#{version}/Baobar-#{version}-macOS.zip"
  name "Baobar"
  desc "Menu bar indicator for OpenBao login state and token expiry"
  homepage "https://github.com/BrutalSystems/baobar"

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
