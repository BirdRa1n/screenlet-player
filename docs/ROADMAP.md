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

## v0.3.0 / v0.4.0 — Pairing, sync and telemetry (done, ahead of v0.2.0)

Implemented before real playback because it only required the existing
`NoopPlayer` to prove out end-to-end — see `docs/PAIRING.md` for the full
design. `playback.Player.Play` is already wired to whatever `sync.Poller`
receives, so v0.2.0's mpv backend is a drop-in away from going live on a
paired device.

- [x] Pairing flow per `docs/PAIRING.md` (device announces via heartbeat,
      claimed from Screenlet Studio's Dispositivos panel — no code typed)
- [x] `sync.Client` / `sync.Poller` implementing `Syncer`: polls Studio for
      `ChannelAssignment` and triggers `playback.Player.Play` on change —
      no restart required
- [x] `telemetry.HTTPReporter` implementing `Reporter`: periodic heartbeat
      to Studio, doubling as the pairing announce
- [x] Studio-side: `/api/player/{sync,heartbeat}` routes on the existing
      IPTV server (port 7095) + Dispositivos panel section listing
      detected/paired Screenlet Player devices (Kodi devices keep the
      existing SSH-based flow, unaffected)
- [ ] Studio's online/offline indicator for Player devices (heartbeat data
      is there; panel currently shows it but hasn't been tuned for a
      specific staleness threshold)

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
