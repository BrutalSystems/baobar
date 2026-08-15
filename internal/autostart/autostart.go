// Package autostart registers Baobar to start when the user logs in.
//
// Each platform gets one small artifact. The interface deliberately reports
// real on-disk state rather than a remembered flag: a checkbox showing "on"
// for something that never happened is worse than no checkbox at all.
package autostart

import (
	"errors"
	"os"
	"path/filepath"
)

type Autostart interface {
	// Enabled reports whether the entry currently exists.
	Enabled() (bool, error)
	// Enable creates or replaces the entry, pointing at this executable.
	Enable() error
	// Disable removes the entry. Removing an absent entry is not an error.
	Disable() error
}

// fileAutostart covers every platform whose mechanism is "write a file".
type fileAutostart struct {
	path   string
	exe    func() (string, error)
	render func(exe string) []byte
}

func (a *fileAutostart) Enabled() (bool, error) {
	_, err := os.Stat(a.path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (a *fileAutostart) Enable() error {
	exe, err := a.exe()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(a.path, a.render(exe), 0o644)
}

func (a *fileAutostart) Disable() error {
	err := os.Remove(a.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
