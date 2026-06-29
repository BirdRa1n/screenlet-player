# Pairing

> **Status:** discovery/sync (v0.3.0–v0.4.0) and the authenticated claim
> flow (v0.5.0) are both implemented — see `docs/ROADMAP.md`.
> `internal/sync.Client`/`Poller` and `internal/telemetry.HTTPReporter`
> are the player-side sync/announce pieces; `internal/api` is the
> player-side claim/control surface; Screenlet Studio's IPTV server
> (port 7095) hosts the `/api/player/*` routes described below.

## Problem

Screenlet Studio's Dispositivos panel registers Kodi devices by asking an
admin to type in IP, SSH credentials and Kodi HTTP credentials — because
Studio has no way to discover a Kodi box on its own. Screenlet Player does
better: the player itself can be discovered (two ways, below) and claimed
without typing any of that in by hand.

## Two ways to be found

1. **Heartbeat (push).** If started with `-studio-url` — or a Studio URL
   persisted from an earlier claim, see below — the player POSTs a
   `telemetry.Heartbeat` (device ID, hostname, pairing code, player
   version, playback state) to `{studioURL}/api/player/heartbeat` every
   20s. Studio's Dispositivos panel lists devices that heartbeat but
   aren't paired yet ("detectados na rede"), showing hostname + pairing
   code so an admin can tell two devices apart.
2. **Network scan (pull).** Studio can sweep its local subnet(s),
   probing `GET http://<ip>:8089/identify` on each host. A compatible,
   unclaimed player responds `{deviceId, hostname, playerVersion,
   claimed:false}` with no prior heartbeat or `-studio-url` needed —
   the only way to find a device that's never been told where Studio is.

Either way, Studio ends up with the device's reachable address before
claiming it.

## Claiming

Claiming does two things atomically, not just "Studio remembers a
channel choice":

1. Studio `POST`s `{"studioUrl": "..."}` (optional) to the device's own
   `http://<ip>:8089/claim`.
2. The device mints a random 32-byte token
   (`internal/device.GenerateAPIToken`), persists it — and `studioUrl`,
   if given — to local storage, and returns `{token, deviceId,
   pairingCode}`. This can only succeed once: a second `/claim` call
   gets `409 Conflict` until a local `-reset` (see below).
3. Studio encrypts and stores that token (same `safeStorage` pattern as
   the Kodi bridge's SSH/HTTP credentials) and sends it as
   `Authorization: Bearer <token>` on every future call to that device's
   control API.
4. If the device had no Studio URL yet — claimed purely via network
   scan, never given `-studio-url` — receiving one in the claim request
   starts its sync/telemetry goroutines immediately, no restart needed.
5. Studio's Dispositivos panel then assigns a channel, same as before.
6. **Sync**, independent of all of the above: the player polls
   `GET {studioURL}/api/player/sync?deviceId=...` every 15s
   (`internal/sync.Poller`), which replies with the assigned channel
   once one exists. The reply carries an offline **manifest** — the
   ordered assets (filename, download URL, byte size, `sha256` hash,
   transition) plus a `version` digest of the whole list. The player
   downloads and verifies those into its local cache (`internal/media`)
   and plays them from disk, so a reboot with no reachable server still
   shows the last channel. Change detection keys on `version`, so an
   in-place re-render (same filename, new bytes) is still picked up.
   Updating a channel in Studio reaches the device on its next sync tick
   automatically — no restart required, unlike the Kodi bridge. The
   legacy `playlistUrl` live stream remains as a fallback.

## Security model

Before claiming, a device exposes only `/identify` (read-only, no
secrets — just enough for a network scan to recognize it) and `/claim`
(single-shot). After claiming, `/status`, `/play` and `/stop` all
require the bearer token minted at claim time, compared in constant
time (`crypto/subtle.ConstantTimeCompare`) so a timing side-channel
can't be used to guess it — only the Screenlet Studio instance that
claimed a device can control it.

Un-claiming is **not** exposed over the network on purpose. The only way
to free a device for a different Studio instance is local/SSH access
plus `screenlet-player -reset`, which wipes device identity, pairing
code, token and channel — a fresh device ID is generated on next start.
This is deliberate: losing or wiping a Studio install must not be enough
to hijack a device someone else still physically controls.

The control API still binds all interfaces, not `127.0.0.1` only —
Studio needs to reach it from a different machine on the LAN, which is
the entire point of this API existing. The mitigation here is the
token, not network isolation.

Heartbeat/sync (Studio's `/api/player/*` HTTP routes, as opposed to the
player's own `/identify`/`/claim`/`/status`/`/play`/`/stop`) remain
unauthenticated beyond the device ID, matching the same LAN-trust level
Studio's `playlist.m3u`/`/channel/{id}` endpoints already use. That
wasn't in scope for this pass, which targeted the player's own control
API specifically; revisit together before exposing Studio beyond a
trusted LAN.

**Known residual risk, accepted for now:** everything above runs over
plain HTTP, including the token itself during the claim handshake — see
`docs/ROADMAP.md`. Mitigated only by requiring a trusted LAN; closing it
properly needs a TLS cert strategy (self-signed + pinning) on both sides.

## Resolved from earlier open questions

- ~~Exact transport for claiming~~ → `POST /claim` on the device's own
  control API (see above), not just a Studio-side list click.
- ~~Pairing code expiry~~ → still not implemented; unrelated to the API
  token, which also doesn't expire — only `-reset` revokes it.
- ~~Multi-tenancy~~ → enforced for the control API specifically (one
  token, one owner, by construction). Heartbeat/sync remain
  LAN-scoped/unauthenticated, as noted above.
