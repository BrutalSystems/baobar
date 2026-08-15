//go:build windows

package tray

// systray's Windows backend hands the bytes to LoadImage, which wants an .ico.
// PNG produces no icon at all — and with SetTitle a no-op on Windows, that
// leaves the app with no presence in the tray whatsoever.
const useICO = true
