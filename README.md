# Screenlet Player

Lightweight digital signage player for Linux. Screenlet Player pairs with a
[Screenlet Studio](https://github.com/BirdRa1n/AdFrame) instance, syncs its
assigned channel, caches the channel's media locally, and plays it back
fullscreen — no Kodi, no media-center menus, no SSH bridge required.

> **Status:** functional. Pairing, channel sync, telemetry and the
> mpv-backed playback engine are implemented, along with an authenticated
> control/claim API and (since v0.5.5) offline-first playback: a device keeps
> looping its last channel after a reboot even with no reachable server.
> See [docs/ROADMAP.md](docs/ROADMAP.md).

## Why

Screenlet Studio currently drives signage devices by SSHing into a Kodi
installation and calling its JSON-RPC API — useful as a bridge to existing
hardware, but heavy: a full media center, a manual add-on configuration
step, and a required restart every time a playlist changes. Screenlet
Player is a purpose-built alternative: a single static binary that boots
straight into the assigned channel and exposes a small native control API,
so Studio can manage it the same way it manages any other paired device —
without SSH.

## Architecture

```
cmd/screenlet-player     entry point — wires everything together
internal/
  api                    local HTTP control API (status, play, stop)
  device                 stable device identity, persisted across restarts
  media                  local asset cache + offline playback manifest
  display                output mode detection (resolution, fullscreen)
  playback               Player interface; MPVPlayer (mpv IPC) + NoopPlayer fallback
  sync                    polls Screenlet Studio for the device's channel
  telemetry              periodic heartbeats back to Screenlet Studio
  updater                checks GitHub Releases for newer builds
  storage                local JSON config (device ID, pairing, channel)
pkg/version              build-time version metadata (-ldflags)
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for how these pieces fit
together — including the **offline-first playback** model (cache, manifest,
boot-from-cache) — and how the player talks to Screenlet Studio.

## Requirements

- Go 1.26+
- Linux target: amd64, arm64, or armv7 (e.g. Raspberry Pi) — see
  [docs/INSTALLATION.md](docs/INSTALLATION.md)
- macOS for local development (darwin-arm64 builds are produced for this)

## Installation

Pre-built binaries are published on the
[Releases](https://github.com/BirdRa1n/screenlet-player/releases) page for
`linux-amd64`, `linux-arm64`, `linux-armv7` and `darwin-arm64`.

```bash
curl -fsSL https://raw.githubusercontent.com/BirdRa1n/screenlet-player/main/scripts/install.sh | bash
```

This detects your OS/arch and fetches the right binary from the GitHub
Releases API. A short `player.screenlet.app/install.sh` redirect is
planned once that domain exists (see `docs/ROADMAP.md`) but isn't a
separate implementation — it'll just point here. Full instructions,
including running as a systemd service, are in
[docs/INSTALLATION.md](docs/INSTALLATION.md).

## Development (macOS)

```bash
git clone https://github.com/BirdRa1n/screenlet-player.git
cd screenlet-player

go run ./cmd/screenlet-player          # starts the control API on :8089
go run ./cmd/screenlet-player -version

go fmt ./...
go vet ./...
go test ./...
```

With the binary running, `/status`/`/play`/`/stop` require a bearer
token — claim the device first (`/identify` and `/claim` are the only
unauthenticated routes; see [docs/PAIRING.md](docs/PAIRING.md)):

```bash
curl localhost:8089/identify
TOKEN=$(curl -s -X POST localhost:8089/claim -d '{}' | sed -E 's/.*"token":"([^"]+)".*/\1/')

curl -H "Authorization: Bearer $TOKEN" localhost:8089/status
curl -H "Authorization: Bearer $TOKEN" -X POST localhost:8089/play -d '{"source":"http://example.com/a.m3u"}'
curl -H "Authorization: Bearer $TOKEN" -X POST localhost:8089/stop
```

A device can only be claimed once — run `screenlet-player -reset` to
undo it locally and try again.

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for the full local dev
workflow and project conventions.

## Compiling for Linux

Cross-compile from macOS (no CGO, so no cross-toolchain needed):

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/screenlet-player-linux-amd64 ./cmd/screenlet-player
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o dist/screenlet-player-linux-arm64 ./cmd/screenlet-player
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -o dist/screenlet-player-linux-armv7 ./cmd/screenlet-player
```

Or run [`scripts/build.sh`](scripts/build.sh) to build the full release
matrix (`linux-amd64`, `linux-arm64`, `linux-armv7`, `darwin-arm64`) at once
into `dist/`. The same matrix runs in CI on every tagged release — see
[.github/workflows/release.yml](.github/workflows/release.yml).

## Roadmap

See [docs/ROADMAP.md](docs/ROADMAP.md) for the versioned plan from this
scaffold (`v0.1.0`) through pairing, mpv-backed playback, and a `v1.0.0`
general release.

## License

[MIT](LICENSE)
