//go:build windows

package helperapp

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

type registryAutoStarter struct{}

func NewAutoStarter() AutoStarter {
	return registryAutoStarter{}
}

func (registryAutoStarter) IsEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetStringValue("UAPI Helper")
	return err == nil && value != ""
}

func (registryAutoStarter) SetEnabled(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !enabled {
		return key.DeleteValue("UAPI Helper")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return key.SetStringValue("UAPI Helper", `"`+exe+`"`)
}
