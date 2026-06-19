# Pairing

> **Status:** design only — not implemented yet. `internal/sync` defines
> the `Syncer` interface this depends on, but there is no pairing
> endpoint in `internal/api` or Studio yet. This document describes the
> intended flow so the implementation has a target.

## Problem

Screenlet Studio's Dispositivos panel currently registers Kodi devices by
asking an admin to type in IP, SSH credentials and Kodi HTTP credentials —
because Studio has no way to discover a Kodi box on its own. Screenlet
Player can do better: the player itself can initiate contact with Studio,
the same way a smart TV app pairs with a phone.

## Intended flow

1. **First boot.** The player generates a random device ID
   (`internal/device`) and persists it (`internal/storage`). It has no
   Studio URL yet, so it has nothing to sync.
2. **Pairing code.** The player generates a short, human-typeable code
   (e.g. 6 characters) and displays it — once `internal/display` /
   playback land, this shows fullscreen; until then it's logged to
   stdout/journald.
3. **Admin enters the code.** In Screenlet Studio's Dispositivos panel,
   the admin clicks "Pair device" and types the code, instead of
   filling in IP/SSH/Kodi credentials.
4. **Studio resolves the code.** Studio looks up which device most
   recently advertised that code (devices poll a `/pair/claim`-style
   endpoint, or Studio pushes to a well-known local port — exact
   transport TBD) and links that device ID to a channel.
5. **Player starts syncing.** Once paired, `internal/sync.Syncer` begins
   polling Studio for the device's `ChannelAssignment` (channel ID +
   playlist URL) at a regular interval, and `internal/telemetry.Reporter`
   begins sending heartbeats so the device shows as online.
6. **Channel changes.** Because the player polls instead of waiting for
   an SSH-triggered restart, a playlist update on Studio reaches the
   device on the next sync tick — no restart required, unlike the
   current Kodi bridge.

## Why pull, not push

The legacy Kodi bridge is push-based: Studio SSHes into a device whenever
a channel it owns is saved. That requires Studio to hold SSH credentials
for every device and reach them over the network on demand. A pull-based
design flips this: the device only needs outbound HTTPS to Studio, which
is friendlier to NAT/firewalled signage networks and removes the need to
store SSH passwords for Player devices at all (see Studio's existing
`safeStorage`-encrypted credential handling for the Kodi bridge, which
this design avoids needing in the first place).

## Open questions

- Exact transport for step 4 (short-poll vs. Studio-initiated callback).
- Pairing code expiry and re-pairing after a factory reset.
- Multi-tenancy: can one Studio instance pair devices across networks it
  doesn't share a LAN with, or is pairing LAN-scoped initially?

These will be resolved when pairing is scheduled — see `docs/ROADMAP.md`.
