// Command screenlet-player is the Screenlet Player binary: a lightweight
// digital signage client for Linux that pairs with a Screenlet Studio
// instance, syncs its assigned channel, and plays it back fullscreen.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"strings"
	stdsync "sync"
	"syscall"
	"time"

	"github.com/BirdRa1n/screenlet-player/internal/api"
	"github.com/BirdRa1n/screenlet-player/internal/device"
	"github.com/BirdRa1n/screenlet-player/internal/media"
	"github.com/BirdRa1n/screenlet-player/internal/playback"
	"github.com/BirdRa1n/screenlet-player/internal/sync"
	"github.com/BirdRa1n/screenlet-player/internal/telemetry"
	"github.com/BirdRa1n/screenlet-player/pkg/version"
)

const (
	syncInterval      = 15 * time.Second
	heartbeatInterval = 20 * time.Second
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	reset := flag.Bool("reset", false, "wipe all local pairing state (device identity, API token, channel) so this device can be claimed by a different Screenlet Studio instance — requires local/SSH access, exits without starting the player")
	addr := flag.String("addr", ":8089", "address for the local control API")
	studioURL := flag.String("studio-url", "", "Screenlet Studio base URL, e.g. http://192.168.1.10:7095 (enables pairing + sync; not required if the device will be claimed via network scan instead)")
	mpvBin := flag.String("mpv-bin", "mpv", "mpv executable to use for playback")
	mpvArgs := flag.String("mpv-args", "", "extra space-separated arguments appended to the mpv invocation, e.g. \"--vo=drm\" on a Raspberry Pi")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return
	}

	if *reset {
		if err := device.Reset(); err != nil {
			log.Fatalf("screenlet-player: reset failed: %v", err)
		}
		if err := media.Clear(); err != nil {
			log.Printf("screenlet-player: failed to clear media cache during reset: %v", err)
		}
		fmt.Println("screenlet-player: local state wiped. This device is unclaimed again — it will generate a new identity and pairing code on next start.")
		return
	}

	id, err := device.LoadOrCreate()
	if err != nil {
		log.Fatalf("screenlet-player: failed to load device identity: %v", err)
	}
	log.Printf("screenlet-player %s starting — device %s (%s)", version.Version, id.ID, id.Hostname)

	token, err := device.APIToken()
	if err != nil {
		log.Fatalf("screenlet-player: failed to load API token: %v", err)
	}
	if code, err := device.PairingCode(); err == nil {
		log.Printf("pairing code: %s — shown in Screenlet Studio's Dispositivos panel if discovered via heartbeat", code)
	}

	player := newPlayer(*mpvBin, *mpvArgs)
	defer player.Close()

	// The media cache is what makes playback survive a reboot with no server.
	// If it can't be created we degrade to live streaming rather than fail.
	cache, err := media.NewCache()
	bootedVersion := ""
	if err != nil {
		log.Printf("media cache unavailable (%v) — offline playback disabled, will stream live", err)
		cache = nil
	} else {
		bootedVersion = playCachedAtBoot(cache, player)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var integrationOnce stdsync.Once
	startIntegration := func(url string) {
		if url == "" {
			return
		}
		integrationOnce.Do(func() {
			go runStudioIntegration(ctx, url, id, player, cache, bootedVersion)
		})
	}

	apiSrv := api.New(player, api.Options{
		Info:  api.Info{DeviceID: id.ID, Hostname: id.Hostname, PlayerVersion: version.Version},
		Token: token,
		Mint: func(studioURL string) (api.ClaimResult, error) {
			newToken, err := device.GenerateAPIToken(studioURL)
			if err != nil {
				return api.ClaimResult{}, err
			}
			code, err := device.PairingCode()
			if err != nil {
				return api.ClaimResult{}, err
			}
			return api.ClaimResult{Token: newToken, DeviceID: id.ID, PairingCode: code}, nil
		},
		OnClaimed: func(_ string, studioURL string) {
			log.Println("device claimed by Screenlet Studio")
			startIntegration(studioURL)
		},
	})

	go func() {
		log.Printf("control API listening on %s", *addr)
		if err := http.ListenAndServe(*addr, apiSrv.Handler()); err != nil {
			log.Fatalf("control API: %v", err)
		}
	}()

	switch {
	case *studioURL != "":
		startIntegration(*studioURL)
	case token != "":
		persistedURL, err := device.StudioURL()
		if err != nil {
			log.Printf("failed to load persisted Studio URL: %v", err)
		}
		startIntegration(persistedURL)
	default:
		log.Println("not claimed yet — waiting to be discovered and claimed by Screenlet Studio (or pass -studio-url)")
	}

	<-ctx.Done()
	log.Println("shutting down")
}

// newPlayer tries to start a real mpv-backed playback backend. mpv being
// unavailable (not installed, no usable video output) isn't fatal — the
// control API and pairing/sync still work without it, just rendering
// nothing, which is the expected state on a CI runner or a developer's
// Mac with no mpv installed.
func newPlayer(bin, extraArgs string) playback.Player {
	opts := playback.MPVOptions{BinPath: bin}
	if extraArgs != "" {
		opts.ExtraArgs = strings.Fields(extraArgs)
	}

	player, err := playback.NewMPVPlayer(opts)
	if err != nil {
		log.Printf("mpv playback unavailable (%v) — falling back to a no-op player", err)
		return playback.NewNoopPlayer()
	}
	log.Println("mpv playback backend ready")
	return player
}

// runStudioIntegration keeps this device paired and in sync with a
// Screenlet Studio instance: heartbeats report health (and, before this
// device is claimed, are how Studio's heartbeat-based discovery learns it
// exists) and the poller picks up channel changes without ever needing a
// restart. Started either at boot (-studio-url or a previously persisted
// Studio URL) or on demand, the moment a /claim call supplies one.
func runStudioIntegration(ctx context.Context, studioURL string, id *device.Identity, player playback.Player, cache *media.Cache, bootedVersion string) {
	code, err := device.PairingCode()
	if err != nil {
		log.Printf("pairing: failed to load pairing code for telemetry: %v", err)
	}

	// Tracks the manifest version currently on screen so an unchanged sync
	// doesn't restart playback. Seeded with whatever boot already played from
	// cache, so the first online tick of the same channel is a no-op visually.
	// The poller invokes onChange serially, so a plain variable is enough.
	playingVersion := bootedVersion

	syncClient := sync.NewClient(studioURL, id.ID)
	poller := sync.NewPoller(syncClient)
	if err := poller.Start(syncInterval, func(assignment sync.ChannelAssignment) {
		alreadyPlaying := assignment.HasItems() && assignment.Version != "" && assignment.Version == playingVersion
		if applied := onAssignmentChange(ctx, assignment, player, cache, alreadyPlaying); applied != "" {
			playingVersion = applied
		}
	}); err != nil {
		log.Printf("sync: failed to start: %v", err)
	}
	defer poller.Stop()

	reporter := telemetry.NewHTTPReporter(studioURL)
	if err := reporter.Start(heartbeatInterval, func() telemetry.Heartbeat {
		status, _ := player.Status()
		return telemetry.Heartbeat{
			DeviceID:      id.ID,
			Hostname:      id.Hostname,
			PairingCode:   code,
			PlayerVersion: version.Version,
			Playing:       status.Playing,
			Source:        status.Source,
			SentAt:        time.Now().UTC(),
		}
	}); err != nil {
		log.Printf("telemetry: failed to start: %v", err)
	}
	defer reporter.Stop()

	<-ctx.Done()
}

// playCachedAtBoot starts playing the last known channel straight from the
// local cache, before any network contact. This is what keeps a screen filled
// after a reboot when the server is unreachable: if there is a persisted
// manifest and its files are present, we loop them immediately. It returns the
// version it started playing (empty if nothing played) so the sync loop can
// avoid needlessly restarting playback when the server reports the same one.
func playCachedAtBoot(cache *media.Cache, player playback.Player) string {
	manifest, ok, err := media.LoadManifest()
	if err != nil {
		log.Printf("media: failed to load cached manifest: %v", err)
		return ""
	}
	if !ok {
		return "" // nothing cached yet — first boot, or never synced
	}
	paths := cache.LocalPlaylist(manifest)
	if len(paths) == 0 {
		log.Printf("media: cached manifest for channel %s has no usable local files yet", manifest.ChannelID)
		return ""
	}
	log.Printf("playing cached channel %s (%d item(s)) from local storage", manifest.ChannelID, len(paths))
	if err := player.PlayPlaylist(paths, manifest.Loop); err != nil {
		log.Printf("play cached: %v", err)
		return ""
	}
	return manifest.Version
}

// onAssignmentChange reacts to a new channel assignment from Studio. When the
// assignment carries a downloadable manifest it caches the files (verifying and
// persisting them) and plays them locally, so a later reboot needs no server.
// alreadyPlaying suppresses the actual playback swap when that exact version is
// already on screen (e.g. the first online tick after a cache-backed boot),
// while still refreshing the cache. It returns the manifest version now playing
// locally, or "" when it fell back to the legacy live stream.
func onAssignmentChange(ctx context.Context, a sync.ChannelAssignment, player playback.Player, cache *media.Cache, alreadyPlaying bool) string {
	if cache != nil && a.HasItems() {
		manifest := a.Manifest()
		paths, err := cache.Sync(ctx, &manifest)
		if err != nil {
			log.Printf("media: sync completed with errors: %v", err)
		}
		if len(paths) > 0 {
			if err := media.SaveManifest(&manifest); err != nil {
				log.Printf("media: failed to persist manifest: %v", err)
			}
			if alreadyPlaying {
				log.Printf("channel %s already playing latest (%s) from cache; refreshed %d item(s)", manifest.ChannelID, manifest.Version, len(paths))
				return manifest.Version
			}
			log.Printf("channel %s updated (%s): playing %d cached item(s) locally", manifest.ChannelID, manifest.Version, len(paths))
			if err := player.PlayPlaylist(paths, manifest.Loop); err != nil {
				log.Printf("play: %v", err)
			}
			return manifest.Version
		}
		log.Printf("media: no local files available for channel %s, falling back to live stream", a.ChannelID)
	}

	log.Printf("channel assignment changed: %s -> %s", a.ChannelID, a.PlaylistURL)
	if err := player.Play(a.PlaylistURL); err != nil {
		log.Printf("play: %v", err)
	}
	return ""
}
