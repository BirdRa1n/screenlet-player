# Architecture

## Context

[Screenlet Studio](https://github.com/BirdRa1n/AdFrame) is the authoring and
control-plane app: it manages channels, playlists, schedules and (today) an
IPTV server that Kodi clients connect to. Devices are currently driven by
SSHing in and calling Kodi's JSON-RPC API — see Studio's `Dispositivos`
panel. That works as a bridge to existing hardware, but it's heavy: a full
media center, a manual PVR add-on configuration step per device, and a
required Kodi restart every time a playlist changes (Kodi only reads its
M3U source on startup).

Screenlet Player removes that bridge. It is a single static binary that:

1. Generates and persists a stable device identity on first run.
2. Pairs with a Screenlet Studio instance (planned — see `docs/PAIRING.md`).
3. Polls Studio for its assigned channel and plays it back fullscreen.
4. Reports health back to Studio so the Dispositivos panel can show
   online/offline state without SSH.
5. Exposes a small local HTTP API so Studio (or an admin) can control
   playback directly, instead of through Kodi's JSON-RPC surface.

## Component map

```
cmd/screenlet-player     entry point — wires the pieces below together
internal/storage         local JSON config: device ID, pairing, channel
internal/device          stable device identity (built on storage)
internal/sync            polls Studio for the device's channel assignment
internal/playback        Player interface; MPVPlayer (mpv IPC) + NoopPlayer fallback
internal/display         output mode detection (resolution, fullscreen)
internal/api             local HTTP control API (status / play / stop)
internal/telemetry       periodic heartbeats back to Studio
internal/updater         checks GitHub Releases for newer builds
pkg/version               build-time version metadata (-ldflags)
```

Dependency direction is one-way: `cmd` depends on everything, `internal/*`
packages depend on `internal/storage` and each other only where the map
above shows an arrow (e.g. `device` → `storage`). No package outside
`cmd` imports the API or wiring logic — each `internal/*` package is
independently testable, which is why `go test ./...` already exercises
`storage`, `device`, `playback` and `api` with real (not mocked) behavior.

## Control API

The local HTTP API is intentionally small. It exists so Studio's
Dispositivos panel can manage a Screenlet Player device the same way it
manages a Kodi device today, minus SSH — and, since v0.5.0, it's how a
device is actually claimed, not just controlled:

| Method | Path        | Auth                  | Purpose                                       |
| ------ | ----------- | ---------------------- | ---------------------------------------------- |
| GET    | `/identify` | none                   | `{deviceId, hostname, playerVersion, claimed}` — discovery |
| POST   | `/claim`    | none (single-shot)     | `{"studioUrl": "..."}` (optional) → mints and returns a bearer token; `409` once already claimed |
| GET    | `/status`   | `Bearer <token>`       | Current playback state (playing, source)       |
| POST   | `/play`     | `Bearer <token>`       | `{"source": "http(s)://..."}` — switch playback |
| POST   | `/stop`     | `Bearer <token>`       | Halt playback                                  |

This deliberately stays smaller than Kodi's full JSON-RPC surface — the
player only needs to do one job. `/identify` and `/claim` are the one
place Studio calls *inbound* into the player; everything else
pairing/sync-related is still the player calling *outward* to Studio —
see the next section and `docs/PAIRING.md` for the full claim flow and
security model.

## Talking to Screenlet Studio

Sync and telemetry (`internal/sync`, `internal/telemetry`) are
client-side: the player calls out to routes Screenlet Studio's
existing IPTV server (port 7095, the same one serving `playlist.m3u`)
exposes. This is independent of the Control API above — a device can be
discovered and claimed via network scan without ever having been told
where Studio is; only sync/telemetry need that URL, learned either from
`-studio-url` or from the claim request itself.

| Method | Path                                | Called by         | Purpose                                  |
| ------ | ----------------------------------- | ------------------ | ----------------------------------------- |
| POST   | `/api/player/heartbeat`             | `telemetry.HTTPReporter` | Health ping; also how Studio first learns a device exists |
| GET    | `/api/player/sync?deviceId=...`     | `sync.Client`       | Fetch this device's channel assignment, once paired |

Full flow, including how an admin claims a device, is in
`docs/PAIRING.md`.

## Why mpv, not a bundled media center

The `playback.Player` interface in `internal/playback` is backend-agnostic
on purpose. The real implementation, `MPVPlayer`
(`internal/playback/mpv.go`), drives [mpv](https://mpv.io) over its JSON
IPC socket:

- Hardware-accelerated decode on Raspberry Pi and similar low-power boards.
- A single long-running process per device, controlled over a Unix socket
  — no HTTP/JSON-RPC server to configure inside the player itself. mpv is
  launched once, idle, and every channel change is just a `loadfile`
  command against the same process — no restart, unlike the Kodi+SSH
  bridge this replaces.
- No bundled skin, add-on manager, or general-purpose media center UI —
  Screenlet Player only ever shows one fullscreen stream.

`cmd` tries to start `MPVPlayer` first and falls back to
`playback.NewNoopPlayer()` with a logged warning if mpv isn't installed
or has no usable video output. This keeps the binary buildable, runnable,
and its control API fully testable on a machine with no display attached
(e.g. CI, or a developer's Mac) without making mpv a hard dependency of
the package itself.

## Why no CGO

Builds run with `CGO_ENABLED=0`. This keeps cross-compilation trivial — a
single `GOOS=linux GOARCH=arm64 go build` from macOS, no cross-toolchain —
which is why the release matrix (`linux-amd64`, `linux-arm64`,
`linux-armv7`, `darwin-arm64`) builds entirely on `ubuntu-latest` runners
in CI. If the eventual mpv integration requires CGO (e.g. linking
`libmpv` directly instead of shelling out / using its socket), this
constraint will need revisiting — tracked in `docs/ROADMAP.md`.
