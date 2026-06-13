//go:build linux

package helperapp

import (
	"fmt"
	"os"
	"path/filepath"
)

type desktopAutoStarter struct{}

func NewAutoStarter() AutoStarter {
	return desktopAutoStarter{}
}

func (desktopAutoStarter) IsEnabled() bool {
	_, err := os.Stat(autostartPath())
	return err == nil
}

func (desktopAutoStarter) SetEnabled(enabled bool) error {
	path := autostartPath()
	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=UAPI Helper
Exec=%s
Terminal=false
X-GNOME-Autostart-enabled=true
`, exe)
	return os.WriteFile(path, []byte(content), 0600)
}

func autostartPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(os.Getenv("HOME"), ".config", "autostart", "uapi-helper.desktop")
	}
	return filepath.Join(dir, "autostart", "uapi-helper.desktop")
}
