package autostart

import (
	"os"
	"path/filepath"
)

// New returns an XDG-autostart-backed autostart.
func New() (Autostart, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return &fileAutostart{
		path:   filepath.Join(dir, "autostart", "baobar.desktop"),
		exe:    os.Executable,
		render: renderDesktop,
	}, nil
}
