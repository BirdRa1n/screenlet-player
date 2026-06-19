# Pairing

> **Status:** implemented as of v0.3.0. `internal/sync.Client`/`Poller`
> and `internal/telemetry.HTTPReporter` are the player-side pieces;
> Screenlet Studio's IPTV server (port 7095) hosts the `/api/player/*`
> routes described below.

## Problem

Screenlet Studio's Dispositivos panel registers Kodi devices by asking an
admin to type in IP, SSH credentials and Kodi HTTP credentials — because
Studio has no way to discover a Kodi box on its own. Screenlet Player does
better: the player itself announces its presence to Studio, the same way a
smart TV app shows up in a phone's pairing list.

## How it actually works

1. **First boot.** The player generates a random device ID
   (`internal/device.LoadOrCreate`) and a short, human-readable pairing
   code (`internal/device.PairingCode`, e.g. `G4GWD`), both persisted to
   local storage so they survive restarts.
2. **Heartbeat doubles as announce.** If started with `-studio-url`, the
   player POSTs a `telemetry.Heartbeat` — device ID, hostname, pairing
   code, player version, current playback state — to
   `{studioURL}/api/player/heartbeat` every 20s, starting immediately.
   This is the *only* way Studio learns a device exists; there is no
   separate "announce" call. Studio upserts an entry keyed by device ID
   on every heartbeat, whether or not it's been claimed yet.
3. **Admin claims it.** Studio's Dispositivos panel lists devices that
   have sent a heartbeat but have no channel assigned ("detectados na
   rede"), showing hostname + pairing code so an admin can tell two
   devices apart. Clicking "Pareie" and picking a channel calls Studio's
   `player-device-claim` IPC, which sets that device's `channelId` and
   flips it to paired — no code needs to be typed, since the device is
   already visible and unambiguous in the list.
4. **Sync.** Independently of heartbeats, the player polls
   `GET {studioURL}/api/player/sync?deviceId=...` every 15s
   (`internal/sync.Poller`). Studio replies `{"paired":false}` until
   claimed; once claimed it replies with the assigned channel's ID and
   playlist URL (`http://{studioIp}:7095/channel/{channelId}` — the same
   URL Kodi devices already use). `Poller` only calls back when the
   assignment actually changes, comparing against the channel's real
   `updatedAt`, not request time.
5. **Channel changes.** Because sync is a poll, not a push, updating a
   channel's playlist in Studio reaches the device on its next sync
   tick automatically — no restart required, unlike the Kodi bridge.

## Why pull, not push

The Kodi bridge is push-based: Studio SSHes into a device whenever a
channel it owns is saved. That requires Studio to hold SSH credentials for
every device and reach them on demand. Pull-based sync flips this: the
device only needs outbound HTTP to Studio, which is friendlier to
NAT/firewalled signage networks and needs no stored credentials for Player
devices at all — contrast with Studio's `safeStorage`-encrypted SSH/Kodi
credentials for the Kodi bridge, which this design has no equivalent of.

## Security model (current, intentionally minimal)

The device ID is the only credential — there's no separate auth token.
This matches the trust model the IPTV server's `playlist.m3u` and
`/channel/{id}` endpoints already use (unauthenticated, LAN-only). Anyone
on the LAN who learns a device ID could read its assignment, but cannot
control playback through these endpoints — there's nothing to write.
Revisit before exposing Studio beyond a trusted LAN (see `docs/ROADMAP.md`).

## Resolved from earlier open questions

- ~~Exact transport for claiming~~ → heartbeat-as-announce, no separate
  endpoint; claiming is a Studio-side click, not a device-side call.
- ~~Pairing code expiry~~ → not implemented; the code is a static,
  human-readable label persisted on first run, regenerating it would
  require clearing local storage (effectively a factory reset).
- ~~Multi-tenancy~~ → not addressed; pairing is implicitly LAN-scoped
  because Studio's IPTV server itself is LAN-only today.
