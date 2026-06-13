# UAPI Helper

UAPI Helper is a lightweight tray utility for end users. It is intentionally separate from the UAPI Gateway/Relay server module.

## Behavior

- Starts as a system tray process.
- Shows `Login` only when logged out.
- Shows `Logout` only when logged in.
- Starts a local one-time browser login page from the tray `Login` item.
- Stores server URL and email in the user config directory.
- Stores refresh tokens in the OS keyring.
- Refreshes once after login and then every 30 minutes.
- Does not include a manual refresh menu item.
- Does not display Base URL or API key values in the menu.
- Copies Base URL and the first enabled API key via menu actions.
- Supports per-user autostart:
  - Linux: `~/.config/autostart/uapi-helper.desktop`
  - Windows: `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

## Build

Windows:

```bash
GOOS=windows GOARCH=amd64 go build -o uapi-helper.exe ./cmd/uapi-helper
```

Linux requires AppIndicator development headers for tray integration:

```bash
sudo apt-get install gcc libgtk-3-dev libayatana-appindicator3-dev
go build -o uapi-helper ./cmd/uapi-helper
```

For distributions that still use legacy AppIndicator:

```bash
sudo apt-get install gcc libgtk-3-dev libappindicator3-dev
go build -tags=legacy_appindicator -o uapi-helper ./cmd/uapi-helper
```
