# Roadmap

Versioning follows [SemVer](https://semver.org/). Pre-1.0, minor bumps
(`v0.x.0`) may include breaking changes to the local config format or
control API — there are no compatibility guarantees yet.

## v0.1.0 — Scaffold

- [x] Repository structure (`cmd/`, `internal/*`, `pkg/`, `docs/`, `scripts/`)
- [x] Device identity, persisted locally (`internal/device`, `internal/storage`)
- [x] Local control API skeleton: `/status`, `/play`, `/stop`
      (`internal/api`), backed by a `NoopPlayer`
- [x] CI: `go fmt` / `go vet` / `go test` / `go build` on every push
- [x] Release pipeline: cross-platform build matrix + GitHub Releases

## v0.2.0 — Real playback

- [x] `playback.Player` implementation backed by mpv's JSON IPC socket
      (`internal/playback/mpv.go`): launches mpv idle once, reuses the
      same process across channel changes via `loadfile` — no restart.
      `cmd` now tries `MPVPlayer` first and falls back to `NoopPlayer`
      with a logged warning if mpv isn't installed (e.g. CI, a Mac with
      no mpv), so the control API and pairing/sync are never blocked on it.
      Verified live: built binary, drove `/play` and `/stop` over the real
      control API, watched `position` advance in real time across
      repeated `Play()` calls, then confirmed a SIGTERM cleanly killed
      the mpv subprocess and removed its IPC socket.
- [ ] `internal/display`: detect output resolution and force fullscreen
      (mpv's own `--fullscreen` flag already covers "force fullscreen";
      resolution detection for telemetry/logging purposes is still open)
- [ ] Manual smoke test on at least one Raspberry Pi target (armv7/arm64)
      (`-mpv-args` flag exists for this — e.g. `--vo=drm` — but untested
      on real hardware)

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
- [x] Studio's online/offline indicator for Player devices: paired cards
      show Online/Offline based on `lastSeenAt` within 60s (2 missed
      20s heartbeats), matching the Wifi/WifiOff badge Kodi cards already use

Verified end-to-end against a real Screenlet Studio instance (not just
`httptest`): a `screenlet-player` binary pointed at `-studio-url` showed
up unprompted in the Dispositivos panel, was claimed via the UI, and
picked up the assigned channel's playlist URL on its next sync tick.

## v0.5.0 — Authenticated control API, claim flow, reset

Closes the gap flagged in a security review: before this, `internal/api`
had no authentication at all, and pairing relied solely on Studio
already knowing a device's address via heartbeat. See `docs/PAIRING.md`
for the full design.

- [x] `internal/api`: `/status`, `/play`, `/stop` all require a bearer
      token, compared in constant time; unauthenticated until claimed
- [x] `/identify` (always open, minimal — no secrets) and `/claim`
      (single-shot; `409` on a second attempt) added to `internal/api`
- [x] `internal/device.GenerateAPIToken`/`Reset`: claiming mints a token
      via `crypto/rand`; `screenlet-player -reset` is the only way to
      undo a claim, requires local/SSH access by design
- [x] `/play` validates `source` is an `http(s)://` URL, rejecting
      `file://` and other schemes
- [x] `internal/playback/mpv.go`: IPC socket now lives in a fresh,
      exclusively-owned `0700` temp directory instead of a predictable
      path directly under the shared `os.TempDir()` (closes a TOCTOU gap)
- [x] `scripts/install.sh`: requires mpv on Linux, attempting
      `apt-get`/`dnf`/`pacman` install and failing with clear manual
      instructions if that doesn't work — no more silently falling back
      to `NoopPlayer` in production because mpv was never installed
- [x] Screenlet Studio: network scan (`/identify` sweep) as a second
      discovery path alongside heartbeat, and a real `/claim` call from
      the claim dialog instead of only updating Studio's local list.
      Heartbeat handling also now captures the device's source IP, so
      heartbeat-discovered devices get a reachable address too. Verified
      live: real claim handshake, `safeStorage` encrypt/decrypt
      round-trip, and an authenticated `/status` call all run against a
      real `screenlet-player` binary from a standalone Electron process.

Verified live: claimed a freshly built binary over its real HTTP API end
to end — `/identify` before and after, `/status`/`/play`/`/stop` reject
no-token and wrong-token requests, accept the token `/claim` returned,
`/play` rejects `file://` and accepts a real `http://` URL with `position`
advancing, a second `/claim` gets `409`, and `-reset` followed by a fresh
boot produces a new device ID with `claimed:false` again.

**Known residual risk, accepted for now:** all of the above runs over
plain HTTP, including the token itself during the claim handshake — see
`docs/PAIRING.md`'s security model section. TLS would close this but
needs a cert strategy (self-signed + pinning) on both sides; deferred
rather than silently dropped.

## v0.5.5 — Offline-first playback

Closes the "black screen after a reboot with no server" gap: until now the
player streamed Studio's live IPTV endpoint, so a device that rebooted while
Studio was offline had nothing to show. Playback now runs from a local,
verified asset cache and survives with no network. See `docs/ARCHITECTURE.md`
→ "Offline-first playback".

- [x] `internal/media`: persistent asset cache + manifest. Atomic,
      fsynced, hash-verified downloads; strict filename validation against
      path traversal; declared-size cap against disk-fill; GC of assets a
      channel no longer references
- [x] `internal/playback`: `PlayPlaylist(paths, loop)` plays an ordered list
      of local files via an mpv `loadlist`, looping the channel; respawn
      resumes the playlist, not just a single source
- [x] Offline-first boot in `cmd`: load the persisted manifest and play from
      cache *before* any server contact; background sync downloads only what
      the content `version`/hash changed and hot-swaps the playlist.
      `-reset` also clears the media cache
- [x] `internal/sync`: assignment carries the manifest (items + `version`);
      change detection keys on the content version, so an in-place re-render
      (same filename, new bytes) is still picked up
- [x] Studio-side: `/api/player/sync` returns the per-channel manifest
      (filename, URL, size, `sha256` hash, transition) and the IPTV server
      gained a Range-capable `/exports/<file>` route for downloads; hashes
      are cached by size+mtime so large videos aren't re-hashed every poll

## v0.6.0 — Self-update

- [ ] `updater.Checker` implementation against the GitHub Releases API
- [ ] In-place binary replacement + restart on new version
- [ ] `player.screenlet.app/install.sh` live, matching `scripts/install.sh`

## v1.0.0 — General availability

- [ ] Stable local config format and control API (compatibility
      guarantees begin here)
- [ ] Documented Raspberry Pi provisioning path (flash → boot → pair)
- [ ] Revisit `CGO_ENABLED=0` if the mpv integration needs it
      (see `docs/ARCHITECTURE.md`)
