package tray

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/BrutalSystems/baobar/internal/autostart"
)

// startAtLoginOutcome must derive the checkbox's post-toggle state from ONLY
// the on-disk read, never from what the user clicked (want). A mutant that
// swaps onDisk for want would still pass a naive "success reflects the
// toggle" case, so every case here pins onDisk and want apart, including the
// milestone's twice-called-out failure shape: a toggle that errors while the
// on-disk state stays unchanged.
func TestStartAtLoginOutcomeReflectsOnDiskStateNotTheRequest(t *testing.T) {
	boom := errors.New("boom")

	cases := []struct {
		name        string
		toggleErr   error
		onDisk      bool
		wantChecked bool
		wantAlert   bool
	}{
		{"enable succeeds: checked, no alert", nil, true, true, false},
		{"disable succeeds: unchecked, no alert", nil, false, false, false},
		// Enable() failed; the on-disk state never changed (still false).
		// The box must show UNCHECKED even though the user clicked to turn
		// it on — this is the exact "claims a state it never reached" shape
		// the milestone rejected.
		{"enable fails, on-disk still false: unchecked, alert", boom, false, false, true},
		// Disable() failed; the on-disk state never changed (still true).
		{"disable fails, on-disk still true: checked, alert", boom, true, true, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checked, needsAlert := startAtLoginOutcome(c.toggleErr, c.onDisk)
			if checked != c.wantChecked {
				t.Errorf("checked = %v, want %v", checked, c.wantChecked)
			}
			if needsAlert != c.wantAlert {
				t.Errorf("needsAlert = %v, want %v", needsAlert, c.wantAlert)
			}
		})
	}
}

// A tray built without StartAtLoginEnabled/ToggleStartAtLogin set (tests,
// future embedders) must not panic the UI goroutine on a click.
func TestOptionsStartAtLoginNilFieldsDoNotPanic(t *testing.T) {
	var o Options

	if got := o.startAtLoginEnabled(); got != false {
		t.Errorf("startAtLoginEnabled() with nil field = %v, want false", got)
	}
	if err := o.toggleStartAtLogin(true); err == nil {
		t.Error("toggleStartAtLogin() with nil field = nil error, want a non-nil error")
	}
}

// A refused toggle carries the one thing the user needs to act on: which
// location was rejected and what to do instead. Collapsing that into the
// generic message strands them with a checkbox that will not stay on and no
// stated reason — the failure mode that produced a plist pointing at /tmp.
func TestStartAtLoginAlertExplainsAVolatilePath(t *testing.T) {
	err := fmt.Errorf("%w: /tmp/baobar is a temporary location", autostart.ErrVolatilePath)

	msg := startAtLoginMessage(err)

	if !strings.Contains(msg, "/tmp/baobar") {
		t.Errorf("message does not name the rejected path: %q", msg)
	}
	if msg == "Could not change the setting." {
		t.Error("volatile-path refusal fell back to the generic message")
	}
}

func TestStartAtLoginAlertFallsBackForUnknownErrors(t *testing.T) {
	msg := startAtLoginMessage(errors.New("permission denied"))
	if msg != "Could not change the setting." {
		t.Errorf("startAtLoginMessage = %q — want the generic message", msg)
	}
}
