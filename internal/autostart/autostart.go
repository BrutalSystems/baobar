// Package autostart registers Baobar to start when the user logs in.
//
// Each platform gets one small artifact. The interface deliberately reports
// real on-disk state rather than a remembered flag: a checkbox showing "on"
// for something that never happened is worse than no checkbox at all.
package autostart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Autostart interface {
	// Enabled reports whether the entry currently exists.
	Enabled() (bool, error)
	// Enable creates or replaces the entry, pointing at this executable.
	Enable() error
	// Disable removes the entry. Removing an absent entry is not an error.
	Disable() error
}

// ErrVolatilePath reports an executable whose location will not survive to
// the next login.
var ErrVolatilePath = errors.New("volatile executable path")

// fileAutostart covers every platform whose mechanism is "write a file".
type fileAutostart struct {
	path   string
	exe    func() (string, error)
	render func(exe string) []byte
	// target extracts the program path back out of a rendered entry, so
	// Enabled can check that it still exists. A nil target skips that check.
	target func(entry []byte) (string, bool)
}

func (a *fileAutostart) Enabled() (bool, error) {
	entry, err := os.ReadFile(a.path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if a.target == nil {
		return true, nil
	}
	exe, ok := a.target(entry)
	if !ok {
		// An entry we cannot read back is one we cannot vouch for.
		return false, nil
	}
	if _, err := os.Stat(exe); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (a *fileAutostart) Enable() error {
	exe, err := a.exe()
	if err != nil {
		return err
	}
	exe = resolveIntoBundle(exe)
	if err := checkStablePath(exe); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(a.path, a.render(exe), 0o644)
}

// resolveIntoBundle follows symlinks, but only when doing so lands inside a
// macOS .app bundle.
//
// os.Executable reports the path used to launch the process, symlink and all.
// Homebrew installs Baobar as /opt/homebrew/bin/baobar -> the executable inside
// /Applications/Baobar.app, and macOS will not associate a process with its
// bundle when the executable is reached through a symlink from outside it: the
// app runs, but reports no bundle identifier, no version, and the name "baobar"
// rather than "Baobar". Recording the resolved path fixes that.
//
// The condition is the important half. Resolving unconditionally would be wrong
// wherever the symlink is the stable interface and its target is not: before
// v0.1.7 that same Homebrew symlink pointed into a versioned Caskroom directory,
// and recording its target would have written a login entry that broke at the
// next upgrade. Only a bundle path is known to be version-independent, so only a
// bundle path is followed.
func resolveIntoBundle(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	// Compared lowercased because macOS filesystems are case-insensitive by
	// default, and ToSlash-ed so the check reads the same on every platform.
	norm := strings.ToLower(filepath.ToSlash(resolved))
	if !strings.Contains(norm, ".app/contents/macos/") {
		return path
	}
	return resolved
}

// volatilePrefixes are locations the OS empties out from under a running
// binary. Recording one produces an entry that is valid, loads at login, and
// fails the spawn — the exact EX_CONFIG failure this guard exists to prevent.
var volatilePrefixes = []string{
	"/tmp/",
	"/private/tmp/",
	"/var/folders/", // macOS per-user TMPDIR
	"/private/var/folders/",
	"/appdata/local/temp/", // Windows %TEMP%
	"/go-build",            // `go run` / `go build` scratch output
}

// checkStablePath refuses an executable that will not still be there at the
// next login. The check is on the string, not the filesystem, so it behaves
// identically on every platform and is testable from any one of them.
func checkStablePath(exe string) error {
	// Backslashes are normalised explicitly: filepath.ToSlash is a no-op off
	// Windows, so it would leave a Windows path unmatched everywhere else and
	// make this check pass on macOS while failing in CI.
	norm := strings.ToLower(strings.ReplaceAll(exe, `\`, "/"))
	prefixes := volatilePrefixes
	if tmp := strings.ToLower(strings.ReplaceAll(os.TempDir(), `\`, "/")); tmp != "" && tmp != "/" {
		prefixes = append(append([]string{}, prefixes...), strings.TrimSuffix(tmp, "/")+"/")
	}
	for _, p := range prefixes {
		if strings.Contains(norm, p) {
			// A macOS bundle gets different advice. Gatekeeper runs a
			// quarantined app from a randomised AppTranslocation copy, so the
			// user sees this while running the app from Downloads — and
			// "/usr/local/bin" is meaningless for something they double-clicked.
			// Moving it to /Applications both fixes the path and stops the
			// translocation.
			where := "install Baobar somewhere permanent (for example /usr/local/bin)"
			if strings.Contains(norm, ".app/contents/macos/") {
				where = "move Baobar.app to /Applications"
			}
			return fmt.Errorf("%w: %s is a temporary location that will be empty at your next login; %s and enable this again", ErrVolatilePath, exe, where)
		}
	}
	return nil
}

func (a *fileAutostart) Disable() error {
	err := os.Remove(a.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
