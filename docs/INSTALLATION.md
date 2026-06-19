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
API automatically:

```bash
curl -fsSL https://raw.githubusercontent.com/BirdRa1n/screenlet-player/main/scripts/install.sh | bash
```

## Planned: one-line installer

Once `player.screenlet.app` is live, installation will be:

```bash
curl -fsSL https://player.screenlet.app/install.sh | bash
```

This will be a thin redirect to the same logic as `scripts/install.sh` —
not a separate implementation — so behavior stays identical. Tracked in
`docs/ROADMAP.md`.

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
Screenlet Player as the systemd service above is sufficient once the
real playback backend (mpv) lands — it will take over the display
directly. Until then, the binary only exposes its control API; there is
no fullscreen output yet to autostart into. See `docs/ROADMAP.md`.
