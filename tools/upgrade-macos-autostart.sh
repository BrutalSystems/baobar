#!/usr/bin/env bash
# Repoint an existing "Start at login" entry at Baobar.app, and restart it.
#
# YOU ONLY NEED THIS IF you had "Start at login" enabled before upgrading to
# v0.1.7. Nothing else requires it: a fresh install is already correct, and if
# you never ticked that box there is nothing to repair.
#
# Why it is needed. Before v0.1.7 Baobar was a bare binary at
# /opt/homebrew/bin/baobar, and that is the path your login entry recorded. The
# upgrade turns that path into a symlink into the new app bundle, so the entry
# keeps working — but macOS does not associate a process with its bundle when
# the executable is reached through a symlink from outside it. The result is a
# Baobar that runs correctly while reporting no bundle identity, no version, and
# the name "baobar" rather than "Baobar".
#
# This script rewrites the entry to point at the real path inside the app, then
# restarts it. It is safe to run twice; it backs the entry up before touching it.
#
#   ./upgrade-macos-autostart.sh
#
# See https://github.com/BrutalSystems/baobar/issues/22
set -euo pipefail

LABEL="com.brutalsystems.baobar"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
APP="/Applications/Baobar.app"
TARGET="$APP/Contents/MacOS/baobar"

say()  { printf '%s\n' "$*"; }
fail() { printf 'error: %s\n' "$*" >&2; exit 1; }

[ "$(uname -s)" = "Darwin" ] || fail "this script is for macOS only"

if [ ! -d "$APP" ]; then
  fail "$APP not found. Install or upgrade Baobar first:
    brew upgrade --cask baobar"
fi
[ -x "$TARGET" ] || fail "$TARGET is missing or not executable; the app looks damaged"

if [ ! -f "$PLIST" ]; then
  say "\"Start at login\" is not enabled, so there is nothing to repair."
  say "Nothing was changed."
  exit 0
fi

# Read the path the entry currently runs. PlistBuddy ships with macOS.
current="$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:0' "$PLIST" 2>/dev/null || true)"
[ -n "$current" ] || fail "could not read a program path out of $PLIST"

say "current login entry: $current"

if [ "$current" = "$TARGET" ]; then
  say "Already pointing at the app bundle — no change needed."
else
  backup="$PLIST.bak-$(date +%Y%m%d%H%M%S)"
  cp "$PLIST" "$backup"
  say "backed up to: $backup"

  # Written to match the layout Baobar itself writes, so the "Start at login"
  # tick box still reads it correctly afterwards.
  cat > "$PLIST" <<PLIST_EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>$LABEL</string>
	<key>ProgramArguments</key><array><string>$TARGET</string></array>
	<key>RunAtLoad</key><true/>
</dict>
</plist>
PLIST_EOF
  plutil -lint "$PLIST" >/dev/null || fail "wrote a malformed entry; restore it with: cp '$backup' '$PLIST'"
  say "updated to:  $TARGET"
fi

# Restart so the running copy is the new one. Until this happens the old binary
# stays resident in memory even though its file has been replaced.
#
# bootout is asynchronous: bootstrapping straight after it fails with
# "Bootstrap failed: 5: Input/output error" because the old job is still on its
# way out. So wait for it to actually disappear before bringing it back.
say "restarting Baobar…"

loaded() { launchctl list 2>/dev/null | awk -v l="$LABEL" '$3 == l { found = 1 } END { exit !found }'; }

if loaded; then
  launchctl bootout "gui/$UID/$LABEL" 2>/dev/null || true
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    loaded || break
    sleep 1
  done
  if loaded; then
    fail "the old instance would not stop. Try again, or reboot.
Your entry has been updated, so it will be correct at your next login either way."
  fi
fi

booted=false
for _ in 1 2 3; do
  if launchctl bootstrap "gui/$UID" "$PLIST" 2>/dev/null; then booted=true; break; fi
  sleep 2
done
if [ "$booted" != true ]; then
  fail "could not start the login entry. It is written correctly and will run at
your next login; to start it now, log out and back in."
fi
sleep 3

pid="$(launchctl list | awk -v l="$LABEL" '$3 == l { print $1 }')"
case "$pid" in ''|-) fail "the login entry did not start; check Console.app for $LABEL";; esac

say ""
say "Done. Baobar is running as pid $pid."
if command -v lsappinfo >/dev/null 2>&1; then
  id="$(lsappinfo info -only bundleid "$pid" 2>/dev/null | sed -n 's/.*"CFBundleIdentifier"="\([^"]*\)".*/\1/p')"
  if [ "$id" = "$LABEL" ]; then
    say "macOS now recognises it as the Baobar app ($id)."
  else
    say "Note: macOS still reports no bundle identity for it. Please mention that"
    say "on https://github.com/BrutalSystems/baobar/issues/22"
  fi
fi
