package autostart

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
)

// renderPlist builds a launchd LaunchAgent. exe is XML-escaped: launchd
// silently refuses a plist that is not well-formed, so an unescaped & or <
// in the path would make Enable() report success for something that never
// loads.
func renderPlist(label, exe string) []byte {
	var esc bytes.Buffer
	if err := xml.EscapeText(&esc, []byte(exe)); err != nil {
		return nil
	}
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key><array><string>%s</string></array>
	<key>RunAtLoad</key><true/>
</dict>
</plist>
`, label, esc.String()))
}

// renderDesktop builds an XDG autostart entry. The Exec value is quoted and
// backslash, double quote, backtick and dollar are escaped, per the Desktop
// Entry spec — an unquoted path containing a space is split into separate
// argv tokens.
func renderDesktop(exe string) []byte {
	// Escape backslash, double quote, backtick, and dollar sign per XDG spec
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"`", "\\`",
		"$", "\\$",
	).Replace(exe)
	return []byte(fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Baobar
Exec="%s"
Terminal=false
X-GNOME-Autostart-enabled=true
`, escaped))
}
