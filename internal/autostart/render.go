package autostart

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
)

// label identifies the macOS LaunchAgent. It lives here, not in
// autostart_darwin.go, so that autostart_test.go — which is unconstrained by
// a GOOS build tag and exercises renderPlist directly — compiles on every
// platform. Referencing a darwin-only const from that test file used to
// break `GOOS=linux go vet ./...` and `go test ./...` on Windows/Linux.
const label = "com.brutalsystems.baobar"

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

// plistTarget recovers the program path from a rendered LaunchAgent: the
// first <string> inside ProgramArguments. It decodes rather than pattern
// matches, so an escaped &amp; comes back as the & that launchd will exec.
func plistTarget(entry []byte) (string, bool) {
	dec := xml.NewDecoder(bytes.NewReader(entry))
	inArray := false
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "array":
				inArray = true
			case "string":
				if inArray {
					var s string
					if err := dec.DecodeElement(&s, &t); err != nil {
						return "", false
					}
					return s, true
				}
			}
		case xml.EndElement:
			if t.Name.Local == "array" {
				inArray = false
			}
		}
	}
}

// desktopTarget recovers the program path from a rendered XDG entry by
// undoing renderDesktop's quoting. It is the inverse of that function and
// must stay so: a change to one without the other silently un-checks the
// user's checkbox.
func desktopTarget(entry []byte) (string, bool) {
	for _, line := range strings.Split(string(entry), "\n") {
		rest, ok := strings.CutPrefix(line, `Exec="`)
		if !ok {
			continue
		}
		var b strings.Builder
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case '\\':
				if i+1 >= len(rest) {
					return "", false
				}
				i++
				b.WriteByte(rest[i])
			case '"':
				return b.String(), true
			default:
				b.WriteByte(rest[i])
			}
		}
		return "", false
	}
	return "", false
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
Icon=baobar
Exec="%s"
Terminal=false
X-GNOME-Autostart-enabled=true
`, escaped))
}

// registryTarget recovers the program path from a Windows Run value, undoing
// the quoting Enable applies. It lives here, untagged, so it is testable from
// any platform; the registry access around it is not.
func registryTarget(value string) (string, bool) {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	if value == "" {
		return "", false
	}
	return value, true
}
