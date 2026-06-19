# Roadmap

Versioning follows [SemVer](https://semver.org/). Pre-1.0, minor bumps
(`v0.x.0`) may include breaking changes to the local config format or
control API — there are no compatibility guarantees yet.

## v0.1.0 — Scaffold (current)

- [x] Repository structure (`cmd/`, `internal/*`, `pkg/`, `docs/`, `scripts/`)
- [x] Device identity, persisted locally (`internal/device`, `internal/storage`)
- [x] Local control API skeleton: `/status`, `/play`, `/stop`
      (`internal/api`), backed by a `NoopPlayer`
- [x] CI: `go fmt` / `go vet` / `go test` / `go build` on every push
- [x] Release pipeline: cross-platform build matrix + GitHub Releases

## v0.2.0 — Real playback

- [ ] `playback.Player` implementation backed by mpv's JSON IPC socket
- [ ] `internal/display`: detect output resolution and force fullscreen
- [ ] Manual smoke test on at least one Raspberry Pi target (armv7/arm64)

## v0.3.0 — Pairing and sync

- [ ] Pairing flow per `docs/PAIRING.md` (device-generated code, claimed
      from Screenlet Studio's Dispositivos panel)
- [ ] `sync.Syncer` implementation: polls Studio for `ChannelAssignment`
      and triggers `playback.Player.Play` on change — no restart required
- [ ] Studio-side: pairing UI, replacing manual IP/SSH entry for Player
      devices (Kodi devices keep the existing SSH-based flow)

## v0.4.0 — Telemetry

- [ ] `telemetry.Reporter` implementation: periodic heartbeat to Studio
- [ ] Studio's Dispositivos panel shows online/offline for Player devices
      without polling SSH or Kodi's JSON-RPC

## v0.5.0 — Self-update

- [ ] `updater.Checker` implementation against the GitHub Releases API
- [ ] In-place binary replacement + restart on new version
- [ ] `player.screenlet.app/install.sh` live, matching `scripts/install.sh`

## v1.0.0 — General availability

- [ ] Stable local config format and control API (compatibility
      guarantees begin here)
- [ ] Documented Raspberry Pi provisioning path (flash → boot → pair)
- [ ] Revisit `CGO_ENABLED=0` if the mpv integration needs it
      (see `docs/ARCHITECTURE.md`)
