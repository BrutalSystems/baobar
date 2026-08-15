//go:build !windows

package tray

// macOS and Linux accept PNG, which is what the silhouette tests decode and
// what template rendering expects on macOS.
const useICO = false
