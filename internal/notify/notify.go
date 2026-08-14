package notify

import "github.com/gen2brain/beeep"

// Send shows a desktop notification. Failures are the caller's to ignore:
// a missing notification daemon must never take the tray down.
func Send(title, message string) error {
	return beeep.Notify(title, message, "")
}
