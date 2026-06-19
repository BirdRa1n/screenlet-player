# Installation

## Supported platforms

Pre-built binaries are published for every tagged release:

- `linux-amd64`
- `linux-arm64`
- `linux-armv7` (Raspberry Pi 2/3/4 in 32-bit mode)
- `darwin-arm64` (Apple Silicon — for local development, not signage use)

## Manual install (current)

1. Download the binary for your platform from the
   [latest release](https://github.com/BirdRa1n/screenlet-player/releases/latest).
2. Make it executable and move it onto your `PATH`:

   ```bash
   chmod +x screenlet-player-linux-arm64
   sudo mv screenlet-player-linux-arm64 /usr/local/bin/screenlet-player
   ```

3. Verify it runs:

   ```bash
   screenlet-player -version
   ```

Alternatively, run [`scripts/install.sh`](../scripts/install.sh), which
detects your OS/arch and fetches the right binary from the GitHub Releases
API automatically — this is the one-line installer for now, since
`player.screenlet.app` doesn't exist yet:

```bash
curl -fsSL https://raw.githubusercontent.com/BirdRa1n/screenlet-player/main/scripts/install.sh | bash
```

Once that domain is live, `player.screenlet.app/install.sh` will be a
thin redirect to this same script — not a separate implementation — so
behavior stays identical. Tracked in `docs/ROADMAP.md`.

## Install mpv

Real playback is backed by [mpv](https://mpv.io) over its JSON IPC
socket (`internal/playback/mpv.go`) — install it on the device before
starting the service:

```bash
sudo apt install mpv   # Debian/Raspberry Pi OS
```

If mpv isn't found on `PATH`, Screenlet Player logs a warning and falls
back to a no-op backend: the control API and Studio pairing/sync still
work, but nothing renders. Use `-mpv-bin` / `-mpv-args` (see
`screenlet-player -h`) to point at a non-default mpv binary or pass
hardware-specific flags such as `--vo=drm` on a Raspberry Pi.

## Running as a systemd service

Signage devices should run Screenlet Player as a service that starts on
boot and restarts on failure. Example unit file:

```ini
# /etc/systemd/system/screenlet-player.service
[Unit]
Description=Screenlet Player
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/screenlet-player
Restart=on-failure
RestartSec=2
User=screenlet

[Install]
WantedBy=multi-user.target
```

Enable and start it:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now screenlet-player
sudo journalctl -u screenlet-player -f   # tail logs
```

## Autostart on a kiosk box

For a dedicated signage device (no desktop environment), running
Screenlet Player as the systemd service above is sufficient — mpv takes
over the display directly in fullscreen once a channel is assigned.
Raspberry Pi hardware-accelerated output (`--vo=drm` or similar via
`-mpv-args`) hasn't been smoke-tested on real hardware yet — see
`docs/ROADMAP.md`.
