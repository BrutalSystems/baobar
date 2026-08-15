package autostart

import (
	"errors"
	"os"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const valueName = "Baobar"

type registryAutostart struct{}

// New returns a registry-backed autostart using the per-user Run key, which
// needs no elevation.
func New() (Autostart, error) { return &registryAutostart{}, nil }

func (registryAutostart) Enabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer k.Close()

	value, _, err := k.GetStringValue(valueName)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	// The value existing is not the state the user cares about; the program
	// being there to run is. A Run value pointing at a deleted binary fails
	// silently at login, so it reports as not enabled.
	exe, ok := registryTarget(value)
	if !ok {
		return false, nil
	}
	if _, err := os.Stat(exe); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (registryAutostart) Enable() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := checkStablePath(exe); err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(valueName, `"`+exe+`"`)
}

func (registryAutostart) Disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer k.Close()

	err = k.DeleteValue(valueName)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}
