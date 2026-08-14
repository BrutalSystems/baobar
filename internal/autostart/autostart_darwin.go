package autostart

import (
	"os"
	"path/filepath"
)

const label = "com.brutalsystems.baobar"

// New returns a LaunchAgent-backed autostart. A plist works for an unbundled
// binary; SMAppService would require an app bundle.
func New() (Autostart, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &fileAutostart{
		path:   filepath.Join(home, "Library", "LaunchAgents", label+".plist"),
		exe:    os.Executable,
		render: func(exe string) []byte { return renderPlist(label, exe) },
	}, nil
}
