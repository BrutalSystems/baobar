cask "baobar" do
  version "0.1.8"
  sha256 "ed210c2867d40ace54d5ef09a2b80a0f2adee621e820733caa66b6cc08764b2e"

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

  # launchctl is deliberately NOT here, only in zap below.
  #
  # `brew upgrade` runs the outgoing version's uninstall stanza before
  # installing the new one, and Homebrew's launchctl directive deletes the
  # LaunchAgent plist. Listing it here therefore wipes the user's "Start at
  # login" setting on every single upgrade, silently — Baobar simply stops
  # starting at login and nothing says why. Upgrades are routine; uninstalls
  # are not, so the frequent case wins.
  #
  # The cost is that a plain `brew uninstall` leaves the login entry behind,
  # pointing at an app that is gone. launchd then fails that job at each login,
  # silently and harmlessly. `brew uninstall --zap` removes it properly, and the
  # install guide says so.
  uninstall quit:       "com.brutalsystems.baobar",
            login_item: "Baobar"

  # zap is the complete removal, and the only place launchctl belongs: unload
  # the login job first, then delete the entry it was reading.
  #
  # ~/.vault-token is deliberately absent: Baobar reads and writes it, but it
  # belongs to the `bao` CLI and SOPS. Zapping Baobar must not log you out of
  # your terminal.
  zap launchctl: "com.brutalsystems.baobar",
      trash:     [
        "~/Library/LaunchAgents/com.brutalsystems.baobar.plist",
        "~/Library/Application Support/baobar",
        "~/Library/Caches/baobar",
      ]
end
