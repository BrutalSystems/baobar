package autostart

import (
	"fmt"
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
		path: filepath.Join(home, "Library", "LaunchAgents", label+".plist"),
		exe:  os.Executable,
		render: func(exe string) []byte {
			return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key><array><string>%s</string></array>
	<key>RunAtLoad</key><true/>
</dict>
</plist>
`, label, exe))
		},
	}, nil
}
