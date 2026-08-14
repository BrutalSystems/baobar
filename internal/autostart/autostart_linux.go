package autostart

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// New returns an XDG-autostart-backed autostart.
func New() (Autostart, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return &fileAutostart{
		path: filepath.Join(dir, "autostart", "baobar.desktop"),
		exe:  os.Executable,
		render: func(exe string) []byte {
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
		},
	}, nil
}
